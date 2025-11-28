package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Config struct {
	bind           string
	playerTimeout  time.Duration
	port           int
	prefix         string
	profile        bool
	sessionTimeout time.Duration
	tlsCert        string
	tlsKey         string
	verbose        bool
	version        bool

	// baseURL *url.URL
}

func (c *Config) validate() error {
	if (c.tlsCert == "") != (c.tlsKey == "") {
		return errors.New("both --tls-cert and --tls-key must be provided together")
	}
	if c.port < 1 || c.port > 65535 {
		return fmt.Errorf("invalid port (must be between 1-65535 inclusive): %d", c.port)
	}
	return nil
}

func (c *Config) scheme() string {
	if c.tlsCert != "" && c.tlsKey != "" {
		return "https"
	}
	return "http"
}

func newCmd(cfg *Config) *cobra.Command {
	v := viper.New()
	v.SetEnvPrefix("PARTYBOX")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	cmd := &cobra.Command{
		Use:           "partybox...",
		Short:         "A collection of simple party games, packed in a single modular webapp.",
		Args:          cobra.ExactArgs(0),
		SilenceErrors: true,
		Version:       releaseVersion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cfg.validate(); err != nil {
				return err
			}
			return ServePage(cmd.Context(), cfg, args)
		},
	}

	fs := cmd.Flags()

	fs.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		return pflag.NormalizedName(strings.ReplaceAll(name, "_", "-"))
	})

	fs.StringVarP(&cfg.bind, "bind", "b", "0.0.0.0", "address to bind to (env: PARTYBOX_BIND)")
	fs.DurationVar(&cfg.playerTimeout, "player-timeout", 10*time.Minute, "time before idle players are kicked (env: PARTYBOX_IDLE_PLAYER_TIMEOUT)")
	fs.IntVarP(&cfg.port, "port", "p", 8080, "port to listen on (env: PARTYBOX_PORT)")
	fs.StringVar(&cfg.prefix, "prefix", "", "path to prepend to all URLs, for use behind reverse proxy (env: PARTYBOX_PREFIX)")
	fs.BoolVar(&cfg.profile, "profile", false, "register net/http/pprof handlers (env: PARTYBOX_PROFILE)")
	fs.DurationVar(&cfg.sessionTimeout, "session-timeout", 60*time.Minute, "time before idle game sessions are ended (env: PARTYBOX_IDLE_SESSION_TIMEOUT)")
	fs.StringVar(&cfg.tlsCert, "tls-cert", "", "path to tls certificate (env: PARTYBOX_TLS_CERT)")
	fs.StringVar(&cfg.tlsKey, "tls-key", "", "path to tls keyfile (env: PARTYBOX_TLS_KEY)")
	fs.BoolVarP(&cfg.verbose, "verbose", "v", false, "display additional output (env: PARTYBOX_VERBOSE)")
	fs.BoolVarP(&cfg.version, "version", "V", false, "display version and exit (env: PARTYBOX_VERSION)")

	fs.VisitAll(func(f *pflag.Flag) {
		_ = v.BindPFlag(f.Name, f)
		_ = v.BindEnv(f.Name)
		if !f.Changed && v.IsSet(f.Name) {
			_ = fs.Set(f.Name, fmt.Sprintf("%v", v.Get(f.Name)))
		}
	})

	cmd.CompletionOptions.HiddenDefaultCmd = true
	cmd.SetHelpCommand(&cobra.Command{Hidden: true})
	cmd.SetVersionTemplate("partybox v{{.Version}}\n")

	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	return cmd
}
