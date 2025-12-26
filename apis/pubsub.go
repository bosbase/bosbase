package apis

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"dbx"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/hook"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	"github.com/bosbase/bosbase-enterprise/tools/security"
	"github.com/bosbase/bosbase-enterprise/tools/types"
	"github.com/redis/rueidis"
	"ws"
	"ws/wsutil"
)

const (
	pubSubHubStoreKey     = "__pbPubSubHub"
	pubSubMaxPayloadBytes = 256 * 1024 // arbitrary guard rail
	pubSubCleanupCronKey  = "__pbPubSubCleanup__"
	pubSubMessagesTable   = "_pubsub_messages"
	pubSubRedisChannel    = "pb:pubsub:messages"
)

// bindPubSubApi registers the websocket pub/sub endpoint.
func bindPubSubApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	hub := ensurePubSubHub(app)

	sub := rg.Group("/pubsub")
	sub.GET("", pubSubConnect(hub)).Bind(SkipSuccessActivityLog())
}

func ensurePubSubHub(app core.App) *pubSubHub {
	if cached, ok := app.Store().Get(pubSubHubStoreKey).(*pubSubHub); ok && cached != nil {
		return cached
	}

	hub := newPubSubHub(app)
	app.Store().Set(pubSubHubStoreKey, hub)

	if hub.redis == nil {
		// cleanup task - runs hourly
		app.Cron().Add(pubSubCleanupCronKey, "0 * * * *", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			if err := hub.cleanupBefore(ctx, types.NowDateTime().Add(-24*time.Hour)); err != nil {
				app.Logger().Debug("pubsub cleanup failed", slog.String("error", err.Error()))
			}
		})
	}

	// stop on shutdown/restart
	app.OnTerminate().Bind(&hook.Handler[*core.TerminateEvent]{
		Id: "__pbPubSubStop__",
		Func: func(e *core.TerminateEvent) error {
			hub.Stop()
			return e.Next()
		},
	})

	return hub
}

func pubSubConnect(hub *pubSubHub) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		conn, _, _, err := ws.UpgradeHTTP(e.Request, e.Response)
		if err != nil {
			return e.InternalServerError("Failed to establish websocket connection.", err)
		}

		client := hub.newClient(conn, authIdFromEvent(e))
		hub.addClient(client)
		client.run()

		return nil
	}
}

func authIdFromEvent(e *core.RequestEvent) string {
	if e == nil || e.Auth == nil {
		return ""
	}
	return e.Auth.Id
}

type pubSubHub struct {
	app core.App

	nodeID string

	mu         sync.RWMutex
	clients    map[string]*pubSubClient
	topics     map[string]map[*pubSubClient]struct{}
	redis      *pubSubRedisBridge
	lastCursor pubSubCursor

	pollCtx    context.Context
	pollCancel context.CancelFunc
	pollWG     sync.WaitGroup
	pollEvery  time.Duration
}

func newPubSubHub(app core.App) *pubSubHub {
	node := security.RandomString(10)
	if host, _ := os.Hostname(); host != "" {
		node = host + "-" + node
	}

	hub := &pubSubHub{
		app:       app,
		nodeID:    node,
		clients:   map[string]*pubSubClient{},
		topics:    map[string]map[*pubSubClient]struct{}{},
		pollEvery: 350 * time.Millisecond,
	}

	hub.initRedisBridge()

	return hub
}

func (h *pubSubHub) addClient(c *pubSubClient) {
	h.mu.Lock()
	h.clients[c.id] = c
	h.mu.Unlock()

	if h.redis == nil {
		h.ensurePoller()
	}
}

func (h *pubSubHub) removeClient(c *pubSubClient) {
	h.mu.Lock()
	delete(h.clients, c.id)
	for topic := range c.subs {
		if set := h.topics[topic]; set != nil {
			delete(set, c)
			if len(set) == 0 {
				delete(h.topics, topic)
			}
		}
	}
	remaining := len(h.clients)
	h.mu.Unlock()

	if h.redis == nil && remaining == 0 {
		h.stopPoller()
	}
}

func (h *pubSubHub) Stop() {
	if h.redis != nil {
		h.redis.stop()
	}

	if h.redis == nil {
		h.stopPoller()
	}

	h.mu.RLock()
	clients := make([]*pubSubClient, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, c := range clients {
		c.close()
	}
}

func (h *pubSubHub) initRedisBridge() {
	redisURL := strings.TrimSpace(os.Getenv(redisURLEnvKey))
	if redisURL == "" {
		return
	}

	service := ensureRedisService(h.app, redisURL, strings.TrimSpace(os.Getenv(redisPassEnvKey)))
	client, err := service.getClient()
	if err != nil {
		h.app.Logger().Warn("pubsub redis init failed", slog.String("error", err.Error()))
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	bridge := &pubSubRedisBridge{
		client: client,
		cancel: cancel,
	}

	bridge.wg.Add(1)
	go bridge.run(ctx, h)

	h.redis = bridge
}

func (h *pubSubHub) hasClients() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients) > 0
}

func (h *pubSubHub) ensurePoller() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.pollCtx != nil {
		return
	}

	if h.lastCursor.isZero() {
		if cursor, err := h.fetchLatestCursor(context.Background()); err == nil {
			h.lastCursor = cursor
		} else if !errors.Is(err, sql.ErrNoRows) {
			h.app.Logger().Debug("pubsub cursor init failed", slog.String("error", err.Error()))
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.pollCtx = ctx
	h.pollCancel = cancel
	h.pollWG.Add(1)
	go h.pollLoop(ctx)
}

func (h *pubSubHub) stopPoller() {
	h.mu.Lock()
	cancel := h.pollCancel
	h.pollCtx = nil
	h.pollCancel = nil
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	h.pollWG.Wait()
}

func (h *pubSubHub) pollLoop(ctx context.Context) {
	defer h.pollWG.Done()

	ticker := time.NewTicker(h.pollEvery)
	defer ticker.Stop()

	cursor := h.cursor()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if h.redis != nil || !h.hasClients() {
				continue
			}

			messages, err := h.fetchSince(ctx, cursor)
			if err != nil {
				h.app.Logger().Debug("pubsub poll failed", slog.String("error", err.Error()))
				continue
			}

			for _, msg := range messages {
				cursor = pubSubCursor{ID: msg.ID, Created: msg.Created}
				h.setCursor(cursor)

				if msg.Origin == h.nodeID {
					continue
				}

				h.broadcast(msg)
			}
		}
	}
}

func (h *pubSubHub) cursor() pubSubCursor {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastCursor
}

func (h *pubSubHub) setCursor(c pubSubCursor) {
	h.mu.Lock()
	h.lastCursor = c
	h.mu.Unlock()
}

func (h *pubSubHub) fetchLatestCursor(ctx context.Context) (pubSubCursor, error) {
	result := pubSubCursor{}

	sql := fmt.Sprintf(`
		SELECT [[id]], [[created]]
		FROM {{%s}}
		ORDER BY [[created]] DESC, [[id]] DESC
		LIMIT 1
	`, pubSubMessagesTable)

	err := h.app.DB().NewQuery(sql).WithContext(ctx).One(&result)
	return result, err
}

func (h *pubSubHub) fetchSince(ctx context.Context, cursor pubSubCursor) ([]pubSubRecord, error) {
	where := "1=1"
	params := dbx.Params{}

	if !cursor.isZero() {
		where = "(([[created]] > {:cursorCreated}) OR ([[created]] = {:cursorCreated} AND [[id]] > {:cursorId}))"
		params["cursorCreated"] = cursor.Created
		params["cursorId"] = cursor.ID
	}

	sql := fmt.Sprintf(`
		SELECT [[id]], [[topic]], [[payload]], [[origin]], [[createdBy]], [[created]]
		FROM {{%s}}
		WHERE %s
			AND [[topic]] != ''
		ORDER BY [[created]] ASC, [[id]] ASC
		LIMIT 200
	`, pubSubMessagesTable, where)

	var records []pubSubRecord
	err := h.app.DB().NewQuery(sql).Bind(params).WithContext(ctx).All(&records)
	return records, err
}

func (h *pubSubHub) cleanupBefore(ctx context.Context, cutoff types.DateTime) error {
	_, err := h.app.NonconcurrentDB().NewQuery(fmt.Sprintf(`
		DELETE FROM {{%s}}
		WHERE [[created]] < {:cutoff}
	`, pubSubMessagesTable)).Bind(dbx.Params{"cutoff": cutoff}).WithContext(ctx).Execute()
	return err
}

type pubSubRedisBridge struct {
	client rueidis.Client
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (r *pubSubRedisBridge) publish(ctx context.Context, record pubSubRecord) error {
	payload := redisPayloadFromRecord(record)
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return r.client.Do(ctx, r.client.B().Publish().Channel(pubSubRedisChannel).Message(string(raw)).Build()).Error()
}

func (r *pubSubRedisBridge) run(ctx context.Context, h *pubSubHub) {
	defer r.wg.Done()

	subscribe := r.client.B().Subscribe().Channel(pubSubRedisChannel).Build()

	for {
		err := r.client.Receive(ctx, subscribe, func(msg rueidis.PubSubMessage) {
			var payload redisPubSubPayload
			if err := json.Unmarshal([]byte(msg.Message), &payload); err != nil {
				h.app.Logger().Debug("pubsub redis unmarshal failed", slog.String("error", err.Error()))
				return
			}

			record, err := payload.toRecord()
			if err != nil {
				h.app.Logger().Debug("pubsub redis payload parse failed", slog.String("error", err.Error()))
				return
			}

			if record.Origin == h.nodeID {
				return
			}

			h.broadcast(record)
		})

		if err == nil || ctx.Err() != nil {
			return
		}

		h.app.Logger().Debug("pubsub redis receive failed", slog.String("error", err.Error()))
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (r *pubSubRedisBridge) stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

type redisPubSubPayload struct {
	ID        string          `json:"id"`
	Topic     string          `json:"topic"`
	Payload   json.RawMessage `json:"payload"`
	Origin    string          `json:"origin"`
	Created   string          `json:"created"`
	CreatedBy string          `json:"createdBy,omitempty"`
}

func redisPayloadFromRecord(record pubSubRecord) redisPubSubPayload {
	payload := redisPubSubPayload{
		ID:      record.ID,
		Topic:   record.Topic,
		Payload: record.Payload,
		Origin:  record.Origin,
		Created: record.Created.String(),
	}

	if record.CreatedBy.Valid {
		payload.CreatedBy = record.CreatedBy.String
	}

	return payload
}

func (p *redisPubSubPayload) toRecord() (pubSubRecord, error) {
	created, err := types.ParseDateTime(p.Created)
	if err != nil {
		return pubSubRecord{}, err
	}

	record := pubSubRecord{
		ID:      p.ID,
		Topic:   p.Topic,
		Payload: p.Payload,
		Origin:  p.Origin,
		Created: created,
	}

	if p.CreatedBy != "" {
		record.CreatedBy = sql.NullString{String: p.CreatedBy, Valid: true}
	}

	return record, nil
}

func (h *pubSubHub) handleEnvelope(c *pubSubClient, env pubSubEnvelope) {
	env.Type = strings.TrimSpace(strings.ToLower(env.Type))
	env.Topic = strings.TrimSpace(env.Topic)

	switch env.Type {
	case "ping":
		c.sendJSON(serverEnvelope{Type: "pong", RequestID: env.RequestID})
	case "subscribe":
		if env.Topic == "" {
			c.sendError(env.RequestID, "topic must be set")
			return
		}
		h.addSubscription(c, env.Topic)
		c.sendJSON(serverEnvelope{Type: "subscribed", RequestID: env.RequestID})
	case "unsubscribe":
		if env.Topic == "" {
			h.clearSubscriptions(c)
		} else {
			h.removeSubscription(c, env.Topic)
		}
		c.sendJSON(serverEnvelope{Type: "unsubscribed", RequestID: env.RequestID})
	case "publish":
		if env.Topic == "" {
			c.sendError(env.RequestID, "topic must be set")
			return
		}
		if c.authID == "" {
			c.sendError(env.RequestID, "authentication required to publish")
			return
		}
		if len(env.Data) == 0 {
			env.Data = []byte("null")
		}
		if len(env.Data) > pubSubMaxPayloadBytes {
			c.sendError(env.RequestID, "payload too large")
			return
		}
		if !json.Valid(env.Data) {
			c.sendError(env.RequestID, "payload must be valid JSON")
			return
		}

		msg, err := h.persist(context.Background(), env.Topic, env.Data, c.authID)
		if err != nil {
			c.sendError(env.RequestID, "failed to publish message")
			h.app.Logger().Warn("pubsub publish failed", slog.String("error", err.Error()))
			return
		}

		c.sendJSON(serverEnvelope{
			Type:      "published",
			RequestID: env.RequestID,
			ID:        msg.ID,
			Topic:     msg.Topic,
			Created:   msg.Created.String(),
		})

		h.broadcast(msg)
	default:
		c.sendError(env.RequestID, "unsupported pubsub message type")
	}
}

func (h *pubSubHub) addSubscription(c *pubSubClient, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := c.subs[topic]; ok {
		return
	}

	c.subs[topic] = struct{}{}

	set := h.topics[topic]
	if set == nil {
		set = map[*pubSubClient]struct{}{}
		h.topics[topic] = set
	}
	set[c] = struct{}{}
}

func (h *pubSubHub) removeSubscription(c *pubSubClient, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(c.subs, topic)
	if set := h.topics[topic]; set != nil {
		delete(set, c)
		if len(set) == 0 {
			delete(h.topics, topic)
		}
	}
}

func (h *pubSubHub) clearSubscriptions(c *pubSubClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for topic := range c.subs {
		if set := h.topics[topic]; set != nil {
			delete(set, c)
			if len(set) == 0 {
				delete(h.topics, topic)
			}
		}
	}

	c.subs = map[string]struct{}{}
}

func (h *pubSubHub) broadcast(msg pubSubRecord) {
	envelope := serverEnvelope{
		Type:    "message",
		ID:      msg.ID,
		Topic:   msg.Topic,
		Data:    msg.Payload,
		Created: msg.Created.String(),
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		h.app.Logger().Debug("pubsub marshal failed", slog.String("error", err.Error()))
		return
	}

	h.mu.RLock()
	subscribers := h.topics[msg.Topic]
	targets := make([]*pubSubClient, 0, len(subscribers))
	for client := range subscribers {
		targets = append(targets, client)
	}
	h.mu.RUnlock()

	for _, client := range targets {
		client.queue(payload)
	}
}

func (h *pubSubHub) persist(ctx context.Context, topic string, payload json.RawMessage, createdBy string) (pubSubRecord, error) {
	record := pubSubRecord{
		ID:      security.RandomString(16),
		Topic:   topic,
		Payload: payload,
		Origin:  h.nodeID,
		Created: types.NowDateTime(),
	}

	if createdBy != "" {
		record.CreatedBy = sql.NullString{String: createdBy, Valid: true}
	}

	// Prefer Redis fanout; otherwise persist to Postgres for cross-node syncing.
	if h.redis != nil {
		if err := h.redis.publish(ctx, record); err != nil {
			return record, err
		}
	} else {
		params := dbx.Params{
			"topic":     topic,
			"payload":   string(payload),
			"origin":    h.nodeID,
			"createdBy": nil,
		}

		if createdBy != "" {
			params["createdBy"] = createdBy
		}

		sql := fmt.Sprintf(`
			INSERT INTO {{%s}} ([[topic]], [[payload]], [[origin]], [[createdBy]])
			VALUES ({:topic}, {:payload}::jsonb, {:origin}, {:createdBy})
			RETURNING [[id]], [[topic]], [[payload]], [[origin]], [[createdBy]], [[created]];
		`, pubSubMessagesTable)

		if err := h.app.NonconcurrentDB().NewQuery(sql).Bind(params).WithContext(ctx).One(&record); err != nil {
			return record, err
		}
	}

	return record, nil
}

type pubSubEnvelope struct {
	Type      string          `json:"type"`
	Topic     string          `json:"topic"`
	Data      json.RawMessage `json:"data"`
	RequestID string          `json:"requestId"`
}

type serverEnvelope struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	Topic     string          `json:"topic,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Created   string          `json:"created,omitempty"`
	RequestID string          `json:"requestId,omitempty"`
	ClientID  string          `json:"clientId,omitempty"`
	Message   string          `json:"message,omitempty"`
}

type pubSubRecord struct {
	ID        string          `db:"id"`
	Topic     string          `db:"topic"`
	Payload   json.RawMessage `db:"payload"`
	Origin    string          `db:"origin"`
	CreatedBy sql.NullString  `db:"createdBy"`
	Created   types.DateTime  `db:"created"`
}

type pubSubCursor struct {
	ID      string         `db:"id"`
	Created types.DateTime `db:"created"`
}

func (c pubSubCursor) isZero() bool {
	return c.ID == "" && c.Created.IsZero()
}

type pubSubClient struct {
	id     string
	authID string
	hub    *pubSubHub

	conn      *lockedConn
	send      chan []byte
	subs      map[string]struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func (h *pubSubHub) newClient(conn net.Conn, authID string) *pubSubClient {
	return &pubSubClient{
		id:     security.RandomString(16),
		authID: authID,
		hub:    h,
		conn: &lockedConn{
			Conn: conn,
			mu:   &sync.Mutex{},
		},
		send:   make(chan []byte, 64),
		subs:   map[string]struct{}{},
		closed: make(chan struct{}),
	}
}

func (c *pubSubClient) run() {
	defer c.close()

	c.sendJSON(serverEnvelope{
		Type:     "ready",
		ClientID: c.id,
	})

	go c.writeLoop()

	for {
		payload, err := wsutil.ReadClientText(c.conn)
		if err != nil {
			return
		}

		if len(payload) == 0 {
			continue
		}

		var env pubSubEnvelope
		if err := json.Unmarshal(payload, &env); err != nil {
			c.sendError("", "invalid message format")
			continue
		}

		c.hub.handleEnvelope(c, env)
	}
}

func (c *pubSubClient) writeLoop() {
	for {
		select {
		case msg := <-c.send:
			if err := wsutil.WriteServerText(c.conn, msg); err != nil {
				c.close()
				return
			}
		case <-c.closed:
			return
		}
	}
}

func (c *pubSubClient) queue(msg []byte) {
	select {
	case <-c.closed:
		return
	default:
	}

	select {
	case c.send <- msg:
	default:
		c.hub.app.Logger().Warn("pubsub client send buffer full", slog.String("clientId", c.id))
		c.close()
	}
}

func (c *pubSubClient) sendJSON(env serverEnvelope) {
	payload, err := json.Marshal(env)
	if err != nil {
		return
	}
	c.queue(payload)
}

func (c *pubSubClient) sendError(requestID, message string) {
	c.sendJSON(serverEnvelope{
		Type:      "error",
		RequestID: requestID,
		Message:   message,
	})
}

func (c *pubSubClient) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
		c.hub.removeClient(c)
	})
}

type lockedConn struct {
	net.Conn
	mu *sync.Mutex
}

func (c *lockedConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.Write(p)
}
