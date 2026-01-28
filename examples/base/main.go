package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/bosbase/bosbase-enterprise"
	"github.com/bosbase/bosbase-enterprise/apis"
	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/plugins/ghupdate"
	"github.com/bosbase/bosbase-enterprise/plugins/jsvm"
	"github.com/bosbase/bosbase-enterprise/plugins/migratecmd"
	"github.com/bosbase/bosbase-enterprise/tools/hook"
	"github.com/bosbase/bosbase-enterprise/tools/osutils"
)

func main() {
	app := bosbase.New()

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}

	// ---------------------------------------------------------------
	// Optional plugin flags:
	// ---------------------------------------------------------------

	var hooksDir string
	app.RootCmd.PersistentFlags().StringVar(
		&hooksDir,
		"hooksDir",
		"",
		"the directory with the JS app hooks",
	)

	var hooksWatch bool
	app.RootCmd.PersistentFlags().BoolVar(
		&hooksWatch,
		"hooksWatch",
		true,
		"auto restart the app on pb_hooks file change; it has no effect on Windows",
	)

	var hooksPool int
	app.RootCmd.PersistentFlags().IntVar(
		&hooksPool,
		"hooksPool",
		15,
		"the total prewarm goja.Runtime instances for the JS app hooks execution",
	)

	var migrationsDir string
	app.RootCmd.PersistentFlags().StringVar(
		&migrationsDir,
		"migrationsDir",
		"",
		"the directory with the user defined migrations",
	)

	var automigrate bool
	app.RootCmd.PersistentFlags().BoolVar(
		&automigrate,
		"automigrate",
		true,
		"enable/disable auto migrations",
	)

	var publicDir string
	app.RootCmd.PersistentFlags().StringVar(
		&publicDir,
		"publicDir",
		defaultPublicDir(),
		"the directory to serve static files",
	)

	var indexFallback bool
	app.RootCmd.PersistentFlags().BoolVar(
		&indexFallback,
		"indexFallback",
		true,
		"fallback the request to index.html on missing static path, e.g. when pretty urls are used with SPA",
	)

	app.RootCmd.ParseFlags(os.Args[1:])

	// ---------------------------------------------------------------
	// Plugins and hooks:
	// ---------------------------------------------------------------

	// load jsvm (pb_hooks and pb_migrations)
	jsvm.MustRegister(app, jsvm.Config{
		MigrationsDir: migrationsDir,
		HooksDir:      hooksDir,
		HooksWatch:    hooksWatch,
		HooksPoolSize: hooksPool,
	})

	// migrate command (with js templates)
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		TemplateLang: migratecmd.TemplateLangJS,
		Automigrate:  automigrate,
		Dir:          migrationsDir,
	})

	// GitHub selfupdate
	ghupdate.MustRegister(app, app.RootCmd, ghupdate.Config{})

	// static route to serves files from the provided public dir
	// (if publicDir exists and the route path is not already defined)
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Func: func(e *core.ServeEvent) error {
			if !e.Router.HasRoute(http.MethodGet, "/{path...}") {
				e.Router.GET("/{path...}", apis.Static(os.DirFS(publicDir), indexFallback))
			}

			return e.Next()
		},
		Priority: 999, // execute as latest as possible to allow users to provide their own route
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

func apiworker() bool {
	workerDir := "worker"
	if _, statErr := os.Stat(workerDir); statErr != nil {
		if os.IsNotExist(statErr) {
			workerDir = filepath.Join("examples", "base", "worker")
		} else {
			fmt.Println("Error resolving worker directory:", statErr)
			return true
		}
	}
	absWorkerDir, err := filepath.Abs(workerDir)
	if err != nil {
		fmt.Println("Error resolving worker directory:", err)
		return true
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		script := "call .venv\\Scripts\\activate && python worker.py"
		cmd = exec.Command("cmd", "/C", script)
	} else {
		script := "source .venv/bin/activate && python3 worker.py"
		cmd = exec.Command("bash", "-lc", script)
	}
	cmd.Dir = absWorkerDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Println("Error executing Python script:", err)
	}
	fmt.Println("Output from Python script:", string(output))
	return false
}

// the default pb_public dir location is relative to the executable
func defaultPublicDir() string {
	if osutils.IsProbablyGoRun() {
		return "./pb_public"
	}

	return filepath.Join(os.Args[0], "../pb_public")
}
