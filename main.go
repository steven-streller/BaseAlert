// BaseAlert watches the WeAreOne.FM stations and sends a Pushover
// notification when a favourite DJ is on air, or when any DJ is on air during
// a configured time slot.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `BaseAlert – Push, wenn auf WeAreOne.FM dein DJ auflegt

Aufruf: basealert [Kommando]

  (ohne)    Daemon starten
  now       aktuellen Live-Status aller Sender zeigen
  djs       DJ-Namen und IDs aus dem Sendeplan auflisten
  test      Test-Push senden
  health    Zustand des laufenden Daemons prüfen (für Healthchecks)
  version   Version ausgeben

Konfiguriert wird über Umgebungsvariablen und config.yaml, siehe README.
`

// settings is everything that comes from the environment.
type settings struct {
	Token       string // PUSHOVER_TOKEN
	User        string // PUSHOVER_USER
	Device      string // PUSHOVER_DEVICE
	ConfigPath  string // BASEALERT_CONFIG
	StatePath   string // BASEALERT_STATE, empty disables persistence
	HTTPAddr    string // BASEALERT_HTTP_ADDR, empty disables the HTTP server
	LogLevel    string // BASEALERT_LOG_LEVEL
	LogFormat   string // BASEALERT_LOG_FORMAT
	APIURL      string // BASEALERT_API_URL, for tests
	PushoverURL string // BASEALERT_PUSHOVER_URL, for tests

	Stdout io.Writer
	Stderr io.Writer
}

func loadSettings(lookup func(string) (string, bool), stdout, stderr io.Writer) settings {
	get := func(key, fallback string) string {
		if v, ok := lookup(key); ok {
			return strings.TrimSpace(v)
		}
		return fallback
	}
	return settings{
		Token:       get("PUSHOVER_TOKEN", ""),
		User:        get("PUSHOVER_USER", ""),
		Device:      get("PUSHOVER_DEVICE", ""),
		ConfigPath:  get("BASEALERT_CONFIG", "/config/config.yaml"),
		StatePath:   get("BASEALERT_STATE", "/data/state.json"),
		HTTPAddr:    get("BASEALERT_HTTP_ADDR", ":8080"),
		LogLevel:    get("BASEALERT_LOG_LEVEL", "info"),
		LogFormat:   get("BASEALERT_LOG_FORMAT", "json"),
		APIURL:      get("BASEALERT_API_URL", ""),
		PushoverURL: get("BASEALERT_PUSHOVER_URL", ""),
		Stdout:      stdout,
		Stderr:      stderr,
	}
}

func userAgent() string { return "BaseAlert/" + version }

func main() {
	os.Exit(run(os.Args[1:], loadSettings(os.LookupEnv, os.Stdout, os.Stderr)))
}

func run(args []string, set settings) int {
	command := ""
	if len(args) > 0 {
		command = args[0]
	}
	switch command {
	case "", "run":
		return runDaemon(set)
	case "now":
		return cmdNow(set)
	case "djs":
		return cmdDJs(set)
	case "test":
		return cmdTest(set)
	case "health":
		return cmdHealth(set)
	case "version", "--version":
		fmt.Fprintln(set.Stdout, version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(set.Stdout, usage)
		return 0
	default:
		fmt.Fprintf(set.Stderr, "basealert: unbekanntes Kommando %q\n\n%s", command, usage)
		return 2
	}
}
