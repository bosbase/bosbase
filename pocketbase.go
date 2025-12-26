package pocketbase

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dbx"
	"github.com/fatih/color"
	"github.com/bosbase/bosbase-enterprise/cmd"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/list"
	"github.com/bosbase/bosbase-enterprise/tools/osutils"
	"github.com/spf13/cobra"

	_ "github.com/bosbase/bosbase-enterprise/migrations"
)

var _ core.App = (*BosBase)(nil)

// Version of BosBase
var Version = "(untracked)"

// BosBase defines a BosBase app launcher.
//
// It implements [core.App] via embedding and all of the app interface methods
// could be accessed directly through the instance (eg. BosBase.DataDir()).
type BosBase struct {
	core.App

	devFlag           bool
	dataDirFlag       string
	encryptionEnvFlag string
	postgresURLFlag   string

	queryTimeout    int
	hideStartBanner bool

	// RootCmd is the main console command
	RootCmd *cobra.Command

	postgresDSN string
}

// Config is the BosBase initialization config struct.
type Config struct {
	// hide the default console server info on app startup
	HideStartBanner bool

	// optional default values for the console flags
	DefaultDev           bool
	DefaultDataDir       string // if not set, it will fallback to "./pb_data"
	DefaultEncryptionEnv string
	DefaultPostgresURL   string
	DefaultQueryTimeout  time.Duration // default to core.DefaultQueryTimeout (in seconds)

	// optional DB configurations
	DataMaxOpenConns int                // default to core.DefaultDataMaxOpenConns
	DataMaxIdleConns int                // default to core.DefaultDataMaxIdleConns
	AuxMaxOpenConns  int                // default to core.DefaultAuxMaxOpenConns
	AuxMaxIdleConns  int                // default to core.DefaultAuxMaxIdleConns
	DBConnect        core.DBConnectFunc // default to core.dbConnect
}

// New creates a new BosBase instance with the default configuration.
// Use [NewWithConfig] if you want to provide a custom configuration.
//
// Note that the application will not be initialized/bootstrapped yet,
// aka. DB connections, migrations, app settings, etc. will not be accessible.
// Everything will be initialized when [BosBase.Start] is executed.
// If you want to initialize the application before calling [BosBase.Start],
// then you'll have to manually call [BosBase.Bootstrap].
func New() *BosBase {
	_, isUsingGoRun := inspectRuntime()

	return NewWithConfig(Config{
		DefaultDev: isUsingGoRun,
	})
}

// NewWithConfig creates a new BosBase instance with the provided config.
//
// Note that the application will not be initialized/bootstrapped yet,
// aka. DB connections, migrations, app settings, etc. will not be accessible.
// Everything will be initialized when [BosBase.Start] is executed.
// If you want to initialize the application before calling [BosBase.Start],
// then you'll have to manually call [BosBase.Bootstrap].
func NewWithConfig(config Config) *BosBase {
	// initialize a default data directory based on the executable baseDir
	if config.DefaultDataDir == "" {
		baseDir, _ := inspectRuntime()
		config.DefaultDataDir = filepath.Join(baseDir, "pb_data")
	}

	if config.DefaultQueryTimeout == 0 {
		config.DefaultQueryTimeout = core.DefaultQueryTimeout
	}

	executableName := filepath.Base(os.Args[0])
	requireS3 := false

	pb := &BosBase{
		RootCmd: &cobra.Command{
			Use:     executableName,
			Short:   executableName + " CLI",
			Version: Version,
			FParseErrWhitelist: cobra.FParseErrWhitelist{
				UnknownFlags: true,
			},
			// no need to provide the default cobra completion command
			CompletionOptions: cobra.CompletionOptions{
				DisableDefaultCmd: true,
			},
		},
		devFlag:           config.DefaultDev,
		dataDirFlag:       config.DefaultDataDir,
		encryptionEnvFlag: config.DefaultEncryptionEnv,
		hideStartBanner:   config.HideStartBanner,
	}

	// replace with a colored stderr writer
	pb.RootCmd.SetErr(newErrWriter())

	// parse base flags
	// (errors are ignored, since the full flags parsing happens on Execute())
	pb.eagerParseFlags(&config)

	pb.postgresDSN = normalizePostgresDSN(pb.postgresURLFlag)
	if pb.postgresDSN == "" {
		pb.postgresDSN = normalizePostgresDSN(os.Getenv("SASSPB_POSTGRES_URL"))
	}

	if pb.postgresDSN != "" {
		if config.DBConnect != nil {
			color.HiYellow("custom DBConnect supplied; ignoring PostgreSQL DSN %s", redactPostgresDSN(pb.postgresDSN))
			pb.postgresDSN = ""
		} else {
			config.DBConnect = pb.postgresDBConnect
			color.HiCyan("PostgreSQL multi-node mode enabled; using %s", redactPostgresDSN(pb.postgresDSN))
		}
	}

	if config.DefaultEncryptionEnv == "" {
		if envName := os.Getenv("SASSPB_ENCRYPTION_ENV"); envName != "" {
			config.DefaultEncryptionEnv = envName
		}
	}

	if config.DefaultEncryptionEnv == "" {
		for _, candidate := range []string{
			"POCKETBASE_ENCRYPTION_KEY",
			"BS_ENCRYPTION_KEY",
			"BOSBASE_ENCRYPTION_KEY",
		} {
			if val := os.Getenv(candidate); len(val) >= 32 {
				config.DefaultEncryptionEnv = candidate
				break
			}
		}
	}

	if config.DefaultEncryptionEnv != "" && os.Getenv(config.DefaultEncryptionEnv) == "" {
		const inlineEnvName = "SASSPB_INLINE_ENCRYPTION_KEY"
		if len(config.DefaultEncryptionEnv) >= 32 && !strings.ContainsAny(config.DefaultEncryptionEnv, " \t\n\r") {
			if err := os.Setenv(inlineEnvName, config.DefaultEncryptionEnv); err == nil {
				color.HiYellow("no env var %s found; treating value as inline encryption key", config.DefaultEncryptionEnv)
				config.DefaultEncryptionEnv = inlineEnvName
			}
		}
	}

	if pb.encryptionEnvFlag == "" {
		pb.encryptionEnvFlag = config.DefaultEncryptionEnv
	}

	// initialize the app instance
	pb.App = core.NewBaseApp(core.BaseAppConfig{
		IsDev:            pb.devFlag,
		DataDir:          pb.dataDirFlag,
		EncryptionEnv:    pb.encryptionEnvFlag,
		QueryTimeout:     time.Duration(pb.queryTimeout) * time.Second,
		DataMaxOpenConns: config.DataMaxOpenConns,
		DataMaxIdleConns: config.DataMaxIdleConns,
		AuxMaxOpenConns:  config.AuxMaxOpenConns,
		AuxMaxIdleConns:  config.AuxMaxIdleConns,
		DBConnect:        config.DBConnect,
		RequireS3:        requireS3,
	})

	// hide the default help command (allow only `--help` flag)
	pb.RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	return pb
}

// Start starts the application, aka. registers the default system
// commands (serve, superuser, version) and executes pb.RootCmd.
func (pb *BosBase) Start() error {
	// register system commands
	pb.RootCmd.AddCommand(cmd.NewSuperuserCommand(pb))
	pb.RootCmd.AddCommand(cmd.NewSettingsCommand(pb))
	pb.RootCmd.AddCommand(cmd.NewServeCommand(pb, !pb.hideStartBanner))

	return pb.Execute()
}

func (pb *BosBase) postgresDBConnect(string) (*dbx.DB, error) {
	if pb.postgresDSN == "" {
		return nil, fmt.Errorf("PostgreSQL DSN not configured")
	}

	return dbx.Open("pgx", pb.postgresDSN)
}

func normalizePostgresDSN(raw string) string {
	trimmed := strings.TrimSpace(raw)
	return trimmed
}

func redactPostgresDSN(raw string) string {
	if raw == "" {
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Sprintf("<invalid:%s>", err)
	}

	if u.User == nil {
		return u.Redacted()
	}

	username := u.User.Username()
	password, _ := u.User.Password()

	if username != "" {
		if len(username) > 16 {
			username = username[:7] + "..." + username[len(username)-7:]
		}
	}

	if password != "" {
		u.User = url.UserPassword(username, "redacted")
	} else if username != "" {
		u.User = url.User(username)
	} else {
		u.User = nil
	}

	return u.Redacted()
}

// Execute initializes the application (if not already) and executes
// the pb.RootCmd with graceful shutdown support.
//
// This method differs from pb.Start() by not registering the default
// system commands!
func (pb *BosBase) Execute() error {
	if !pb.skipBootstrap() {
		if err := pb.Bootstrap(); err != nil {
			return err
		}
	}

	done := make(chan bool, 1)

	// listen for interrupt signal to gracefully shutdown the application
	go func() {
		sigch := make(chan os.Signal, 1)
		signal.Notify(sigch, os.Interrupt, syscall.SIGTERM)
		<-sigch

		done <- true
	}()

	// execute the root command
	go func() {
		// note: leave to the commands to decide whether to print their error
		pb.RootCmd.Execute()

		done <- true
	}()

	<-done

	// trigger cleanups
	event := new(core.TerminateEvent)
	event.App = pb
	return pb.OnTerminate().Trigger(event, func(e *core.TerminateEvent) error {
		return e.App.ResetBootstrapState()
	})
}

// eagerParseFlags parses the global app flags before calling pb.RootCmd.Execute().
// so we can have all BosBase flags ready for use on initialization.
func (pb *BosBase) eagerParseFlags(config *Config) error {
	pb.RootCmd.PersistentFlags().StringVar(
		&pb.dataDirFlag,
		"dir",
		config.DefaultDataDir,
		"the BosBase data directory",
	)

	pb.RootCmd.PersistentFlags().StringVar(
		&pb.encryptionEnvFlag,
		"encryptionEnv",
		config.DefaultEncryptionEnv,
		"the env variable whose value of 32 characters will be used \nas encryption key for the app settings (default none)",
	)

	pb.RootCmd.PersistentFlags().BoolVar(
		&pb.devFlag,
		"dev",
		config.DefaultDev,
		"enable dev mode, aka. printing logs and sql statements to the console",
	)

	pb.RootCmd.PersistentFlags().StringVar(
		&pb.postgresURLFlag,
		"postgres-url",
		config.DefaultPostgresURL,
		"PostgreSQL connection URL (enables multi-node storage)",
	)

	pb.RootCmd.PersistentFlags().IntVar(
		&pb.queryTimeout,
		"queryTimeout",
		int(config.DefaultQueryTimeout.Seconds()),
		"the default SELECT queries timeout in seconds",
	)

	return pb.RootCmd.ParseFlags(os.Args[1:])
}

// skipBootstrap eagerly checks if the app should skip the bootstrap process:
// - already bootstrapped
// - is unknown command
// - is the default help command
// - is the default version command
//
// https://github.com/bosbase/bosbase-enterprise/issues/404
// https://github.com/bosbase/bosbase-enterprise/discussions/1267
func (pb *BosBase) skipBootstrap() bool {
	flags := []string{
		"-h",
		"--help",
		"-v",
		"--version",
	}

	if pb.IsBootstrapped() {
		return true // already bootstrapped
	}

	cmd, _, err := pb.RootCmd.Find(os.Args[1:])
	if err != nil {
		return true // unknown command
	}

	for _, arg := range os.Args {
		if !list.ExistInSlice(arg, flags) {
			continue
		}

		// ensure that there is no user defined flag with the same name/shorthand
		trimmed := strings.TrimLeft(arg, "-")
		if len(trimmed) > 1 && cmd.Flags().Lookup(trimmed) == nil {
			return true
		}
		if len(trimmed) == 1 && cmd.Flags().ShorthandLookup(trimmed) == nil {
			return true
		}
	}

	return false
}

// inspectRuntime tries to find the base executable directory and how it was run.
//
// note: we are using os.Args[0] and not os.Executable() since it could
// break existing aliased binaries (eg. the community maintained homebrew package)
func inspectRuntime() (baseDir string, withGoRun bool) {
	if osutils.IsProbablyGoRun() {
		// probably ran with go run
		withGoRun = true
		baseDir, _ = os.Getwd()
	} else {
		// probably ran with go build
		withGoRun = false
		baseDir = filepath.Dir(os.Args[0])
	}
	return
}

// newErrWriter returns a red colored stderr writter.
func newErrWriter() *coloredWriter {
	return &coloredWriter{
		w: os.Stderr,
		c: color.New(color.FgRed),
	}
}

// coloredWriter is a small wrapper struct to construct a [color.Color] writter.
type coloredWriter struct {
	w io.Writer
	c *color.Color
}

// Write writes the p bytes using the colored writer.
func (colored *coloredWriter) Write(p []byte) (n int, err error) {
	colored.c.SetWriter(colored.w)
	defer colored.c.UnsetWriter(colored.w)

	return colored.c.Print(string(p))
}
