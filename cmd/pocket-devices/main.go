// Command pocket-devices keeps your own hardware in one place: add a device,
// connect it with a token and change its settings from here (the NTP Clock
// first). One binary, one SQLite file.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/config"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/kinds"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/server"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/sim"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/store"
	"github.com/abjelosevic88/pocket-homelab-devices/web"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println("pocket-devices", version)
			return
		case "backup":
			runBackup(os.Args[2:])
			return
		case "reset-password":
			runResetPassword(os.Args[2:])
			return
		case "simulate":
			runSimulate(os.Args[2:])
			return
		case "help", "--help", "-h":
			fmt.Println(`pocket-devices — your own hardware in one place

Usage:
  pocket-devices                     start the server (default)
  pocket-devices backup [-o FILE]    write a consistent SQLite backup
  pocket-devices reset-password [-email USER] [-password NEW]
                                   set a user's password (default: first user, random password printed)
  pocket-devices simulate -kind KIND -url URL -id MAC -token TOKEN [-event press:action=double] [-random] [-once]
                                   behave like a device of any kind checking in
  pocket-devices version             print version

Configuration is read from environment variables, see docs/configuration.md.`)
			return
		}
	}
	serve()
}

func newLogger(cfg *config.Config) *slog.Logger {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func mustLoad() (*config.Config, *slog.Logger, *store.Store) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	log := newLogger(cfg)
	st, err := store.Open(cfg.DBPath, log)
	if err != nil {
		log.Error("open database", "path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	return cfg, log, st
}

func serve() {
	server.Version = version
	cfg, log, st := mustLoad()
	defer st.Close()

	var webFS fs.FS
	if sub, err := fs.Sub(web.Dist, "dist"); err == nil {
		if f, err := sub.Open("index.html"); err == nil {
			f.Close()
			webFS = sub
		}
	}
	if webFS == nil {
		log.Warn("web UI not embedded; API only (run `make web` before building)")
	}

	srv := server.New(cfg, st, log, webFS)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := srv.Bootstrap(ctx); err != nil {
		log.Error("bootstrap", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Info("pocket-devices started", "version", version, "addr", cfg.Addr(), "base_url", cfg.BaseURL, "db", cfg.DBPath, "auth_mode", cfg.AuthMode)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, c2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer c2()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func runBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	out := fs.String("o", "", "output file (default: DATA_DIR/backups/pocket-devices-<timestamp>.db)")
	_ = fs.Parse(args)
	cfg, log, st := mustLoad()
	defer st.Close()
	dst := *out
	if dst == "" {
		_ = os.MkdirAll(cfg.DataDir+"/backups", 0o755)
		dst = fmt.Sprintf("%s/backups/pocket-devices-%s.db", cfg.DataDir, time.Now().Format("20060102-150405"))
	}
	if err := st.Backup(context.Background(), dst); err != nil {
		log.Error("backup failed", "err", err)
		os.Exit(1)
	}
	fmt.Println(dst)
}

func runResetPassword(args []string) {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	email := fs.String("email", "", "user email (default: the first user)")
	password := fs.String("password", "", "new password (default: generate a random one)")
	_ = fs.Parse(args)
	_, log, st := mustLoad()
	defer st.Close()
	ctx := context.Background()
	var u *store.User
	var err error
	if *email != "" {
		u, err = st.GetUserByEmail(ctx, *email)
	} else {
		var users []store.User
		users, err = st.ListUsers(ctx)
		if err == nil && len(users) > 0 {
			u = &users[0]
		} else if err == nil {
			err = errors.New("no users found")
		}
	}
	if err != nil {
		log.Error("reset-password: user lookup failed", "err", err)
		os.Exit(1)
	}
	pw := *password
	if pw == "" {
		const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			log.Error("reset-password: random", "err", err)
			os.Exit(1)
		}
		for i := range b {
			b[i] = alphabet[int(b[i])%len(alphabet)]
		}
		pw = string(b)
	}
	if len(pw) < 8 {
		log.Error("reset-password: password must be at least 8 characters")
		os.Exit(1)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		log.Error("reset-password: hash", "err", err)
		os.Exit(1)
	}
	u.PasswordHash = string(h)
	if err := st.UpdateUser(ctx, u); err != nil {
		log.Error("reset-password: save", "err", err)
		os.Exit(1)
	}
	if *password == "" {
		fmt.Printf("Password for %s reset.\nTemporary password: %s\nChange it under Settings → Profile after logging in.\n", u.Email, pw)
	} else {
		fmt.Printf("Password for %s reset.\n", u.Email)
	}
}

// runSimulate behaves like one device of any kind talking to a server, so
// the web UI can be tried without hardware: it checks in every poll interval,
// applies settings with the device's own rules, answers commands and (with
// -event or -random) tells events like button presses or a door opening.
func runSimulate(args []string) {
	fs := flag.NewFlagSet("simulate", flag.ExitOnError)
	kind := fs.String("kind", "ntp-clock", "the kind of device: "+strings.Join(kindIDs(), ", "))
	url := fs.String("url", "http://localhost:8080", "the server (server_url, no trailing slash)")
	id := fs.String("id", "A1B2C3D4E5F6", "the device's MAC address (device_id)")
	token := fs.String("token", "", "the device token shown when the device was added")
	event := fs.String("event", "", "send one event first, e.g. press:action=double, opened, leak")
	random := fs.Bool("random", false, "make up activity (presses, doors, motion) and check in when it happens")
	once := fs.Bool("once", false, "check in once and stop")
	_ = fs.Parse(args)
	if *token == "" {
		fmt.Fprintln(os.Stderr, "simulate: -token is required (add the device in the web UI first)")
		os.Exit(2)
	}
	if _, ok := kinds.Get(*kind); !ok {
		fmt.Fprintln(os.Stderr, "simulate: unknown kind; one of", strings.Join(kindIDs(), ", "))
		os.Exit(2)
	}
	d := sim.New(*kind, strings.ToUpper(*id), time.Now().UnixNano())
	d.Set("ssid", "Simulated")
	d.Set("server_url", strings.TrimRight(*url, "/"))
	d.Set("device_token", *token)
	if *kind == "ntp-clock" {
		d.Set("place", "Banja Luka")
	}
	d.Log = func(s string) { fmt.Println(time.Now().Format("15:04:05"), s) }
	if *event != "" {
		typ, rest, _ := strings.Cut(*event, ":")
		fields := map[string]any{}
		for _, kv := range strings.Split(rest, ",") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				fields[k] = v
			}
		}
		d.Emit(typ, fields)
	}
	cl := &sim.Client{URL: strings.TrimRight(*url, "/"), Token: *token}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	backoff := 5 * time.Second
	next := time.Now()
	for n := 0; ; n++ {
		if !time.Now().Before(next) || d.HasPending() {
			code, wait, err := cl.Sync(ctx, d)
			next = time.Now().Add(time.Duration(d.PollS) * time.Second)
			switch {
			case err != nil:
				fmt.Println(time.Now().Format("15:04:05"), "sync failed:", err)
				next = time.Now().Add(backoff)
				if backoff < 5*time.Minute {
					backoff *= 2
				}
			case code == http.StatusTooManyRequests:
				fmt.Println(time.Now().Format("15:04:05"), "slow down, waiting", wait)
				next = time.Now().Add(wait)
			default:
				backoff = 5 * time.Second
				fmt.Printf("%s checked in: rev %d, next in %ds\n", time.Now().Format("15:04:05"), d.Rev(), d.PollS)
			}
			if *once {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		if *random && n%10 == 9 {
			d.Tick(time.Now())
		}
	}
}

func kindIDs() []string {
	var out []string
	for _, k := range kinds.All() {
		out = append(out, k.ID)
	}
	return out
}
