package migrations

import (
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/types"
)

// Group chat for AI agents and users: chat_rooms and chat_messages are regular
// collections so the records API, the API rules and the realtime subscriptions
// (/api/realtime) work with them; the MCP chat tools use the same records API.
//
// Only the members (and the owner) of a room can read and post its messages and
// a message can only be posted as yourself.
const (
	chatRoomsCollectionId    = "_pb_chat_rooms_"
	chatMessagesCollectionId = "_pb_chat_msgs__"
)

func init() {
	core.SystemMigrations.Add(&core.Migration{
		Up: func(txApp core.App) error {
			users, err := txApp.FindCollectionByNameOrId("users")
			if err != nil {
				return nil // no default users collection to chat with
			}

			if _, err := txApp.FindCollectionByNameOrId("chat_rooms"); err != nil {
				rooms := core.NewBaseCollection("chat_rooms", chatRoomsCollectionId)
				rooms.System = true
				memberRule := `@request.auth.id != "" && (owner = @request.auth.id || members.id ?= @request.auth.id)`
				ownerRule := `@request.auth.id != "" && owner = @request.auth.id`
				rooms.ListRule = types.Pointer(memberRule)
				rooms.ViewRule = types.Pointer(memberRule)
				rooms.CreateRule = types.Pointer(ownerRule)
				rooms.UpdateRule = types.Pointer(ownerRule)
				rooms.DeleteRule = types.Pointer(ownerRule)
				rooms.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 100})
				rooms.Fields.Add(&core.TextField{Name: "description", Max: 1000})
				rooms.Fields.Add(&core.RelationField{Name: "owner", CollectionId: users.Id, Required: true, MaxSelect: 1, CascadeDelete: true})
				rooms.Fields.Add(&core.RelationField{Name: "members", CollectionId: users.Id, MaxSelect: 1000})
				rooms.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
				rooms.Fields.Add(&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})
				if err := txApp.Save(rooms); err != nil {
					return err
				}
			}

			if _, err := txApp.FindCollectionByNameOrId("chat_messages"); err != nil {
				messages := core.NewBaseCollection("chat_messages", chatMessagesCollectionId)
				messages.System = true
				memberRule := `@request.auth.id != "" && (room.owner = @request.auth.id || room.members.id ?= @request.auth.id)`
				messages.ListRule = types.Pointer(memberRule)
				messages.ViewRule = types.Pointer(memberRule)
				messages.CreateRule = types.Pointer(`@request.auth.id != "" && author = @request.auth.id && (room.owner = @request.auth.id || room.members.id ?= @request.auth.id)`)
				messages.UpdateRule = nil // messages are immutable (superusers only)
				messages.DeleteRule = types.Pointer(`@request.auth.id != "" && (author = @request.auth.id || room.owner = @request.auth.id)`)
				messages.Fields.Add(&core.RelationField{Name: "room", CollectionId: chatRoomsCollectionId, Required: true, MaxSelect: 1, CascadeDelete: true})
				messages.Fields.Add(&core.RelationField{Name: "author", CollectionId: users.Id, Required: true, MaxSelect: 1, CascadeDelete: true})
				messages.Fields.Add(&core.TextField{Name: "content", Required: true, Max: 20000})
				messages.Fields.Add(&core.RelationField{Name: "mentions", CollectionId: users.Id, MaxSelect: 50})
				messages.Fields.Add(&core.JSONField{Name: "metadata", MaxSize: 100000})
				messages.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
				messages.AddIndex("idx_chat_messages_room_created", false, "room, created", "")
				if err := txApp.Save(messages); err != nil {
					return err
				}

				// self relation: added once the collection exists
				messages.Fields.Add(&core.RelationField{Name: "replyTo", CollectionId: chatMessagesCollectionId, MaxSelect: 1})
				if err := txApp.Save(messages); err != nil {
					return err
				}
			}

			return nil
		},
		Down: func(txApp core.App) error {
			for _, name := range []string{"chat_messages", "chat_rooms"} {
				if c, err := txApp.FindCollectionByNameOrId(name); err == nil {
					c.System = false
					if err := txApp.Delete(c); err != nil {
						return err
					}
				}
			}
			return nil
		},
		ReapplyCondition: func(txApp core.App, runner *core.MigrationsRunner, fileName string) (bool, error) {
			if _, err := txApp.FindCollectionByNameOrId("users"); err != nil {
				return false, nil
			}
			_, roomsErr := txApp.FindCollectionByNameOrId("chat_rooms")
			_, messagesErr := txApp.FindCollectionByNameOrId("chat_messages")
			return roomsErr != nil || messagesErr != nil, nil
		},
	})
}
