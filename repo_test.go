package main

// Tests that tie the files around the code to the code: the example config,
// the Grafana dashboard and the README.

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"basealert/internal/config"
	"basealert/internal/obs"
)

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := config.Parse([]byte(readRepoFile(t, "config/config.example.yaml")))
	if err != nil {
		t.Fatalf("config/config.example.yaml: %v", err)
	}
	if len(cfg.Stations) == 0 || len(cfg.Favorites) == 0 || len(cfg.Slots) == 0 {
		t.Errorf("the example should show stations, favourites and slots, got %d/%d/%d",
			len(cfg.Stations), len(cfg.Favorites), len(cfg.Slots))
	}
}

func TestComposeExampleFilesExist(t *testing.T) {
	compose := readRepoFile(t, "compose.yaml")
	for _, want := range []string{"env_file: .env", "./config:/config:ro", `["CMD", "/basealert", "health"]`} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose.yaml lacks %q", want)
		}
	}
	env := readRepoFile(t, ".env.example")
	for _, key := range []string{"PUSHOVER_TOKEN=", "PUSHOVER_USER="} {
		if !strings.Contains(env, key) {
			t.Errorf(".env.example lacks %s", key)
		}
	}
	ignored := readRepoFile(t, ".gitignore")
	for _, secret := range []string{".env", "config/config.yaml"} {
		if !slices.Contains(strings.Split(ignored, "\n"), secret) {
			t.Errorf(".gitignore must list %s", secret)
		}
	}
}

// knownSeries returns every series name the catalog can produce.
func knownSeries() map[string]bool {
	known := map[string]bool{}
	for _, info := range obs.Catalog {
		known[info.Name] = true
		if info.Type == "histogram" {
			for _, suffix := range []string{"_bucket", "_sum", "_count"} {
				known[info.Name+suffix] = true
			}
		}
	}
	return known
}

var (
	metricName  = regexp.MustCompile(`basealert_[a-z0-9_]+`)
	eventFilter = regexp.MustCompile(`event=~?"([^"]+)"`)
)

type dashboardFile struct {
	UID        string `json:"uid"`
	Templating struct {
		List []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"list"`
	} `json:"templating"`
	Annotations struct {
		List []struct {
			Expr string `json:"expr"`
		} `json:"list"`
	} `json:"annotations"`
	Panels []struct {
		Title      string `json:"title"`
		Type       string `json:"type"`
		Datasource struct {
			UID string `json:"uid"`
		} `json:"datasource"`
		Targets []struct {
			Expr string `json:"expr"`
		} `json:"targets"`
	} `json:"panels"`
}

// A renamed metric or event would silently empty a panel. This test makes the
// rename fail here instead.
func TestDashboardUsesOnlyKnownMetricsAndEvents(t *testing.T) {
	var dash dashboardFile
	if err := json.Unmarshal([]byte(readRepoFile(t, "grafana/basealert-dashboard.json")), &dash); err != nil {
		t.Fatalf("dashboard is not valid JSON: %v", err)
	}
	if dash.UID != "basealert" {
		t.Errorf("uid = %q", dash.UID)
	}

	variables := map[string]string{}
	for _, v := range dash.Templating.List {
		variables[v.Name] = v.Type
	}
	for name, kind := range map[string]string{"datasource": "datasource", "loki": "datasource", "job": "query", "logs": "textbox"} {
		if variables[name] != kind {
			t.Errorf("variable %q has type %q, want %q", name, variables[name], kind)
		}
	}

	type query struct{ panel, expr string }
	var queries []query
	for _, p := range dash.Panels {
		if p.Type == "row" {
			continue
		}
		if len(p.Targets) == 0 {
			t.Errorf("panel %q has no query", p.Title)
		}
		// No panel may be bound to a concrete data source of one installation.
		if p.Datasource.UID != "${datasource}" && p.Datasource.UID != "${loki}" {
			t.Errorf("panel %q uses data source %q instead of a variable", p.Title, p.Datasource.UID)
		}
		for _, target := range p.Targets {
			queries = append(queries, query{p.Title, target.Expr})
		}
	}
	for _, a := range dash.Annotations.List {
		if a.Expr != "" {
			queries = append(queries, query{"annotation", a.Expr})
		}
	}
	if len(queries) < 20 {
		t.Fatalf("only %d queries found, the dashboard structure has changed", len(queries))
	}

	known := knownSeries()
	usedMetrics, usedEvents := map[string]bool{}, map[string]bool{}
	for _, q := range queries {
		for _, name := range metricName.FindAllString(q.expr, -1) {
			usedMetrics[name] = true
			if !known[name] {
				t.Errorf("panel %q queries unknown metric %s", q.panel, name)
			}
		}
		for _, match := range eventFilter.FindAllStringSubmatch(q.expr, -1) {
			for _, event := range strings.Split(match[1], "|") {
				usedEvents[event] = true
				if !slices.Contains(obs.Events, event) {
					t.Errorf("panel %q filters on unknown event %q", q.panel, event)
				}
			}
		}
		// Queries must follow the job and log selector variables.
		if strings.Contains(q.expr, "basealert_") && !strings.Contains(q.expr, `job="$job"`) {
			t.Errorf("panel %q ignores the job variable: %s", q.panel, q.expr)
		}
		if strings.Contains(q.expr, "| json") && !strings.HasPrefix(q.expr, "${logs:raw}") {
			t.Errorf("panel %q ignores the log selector variable: %s", q.panel, q.expr)
		}
	}
	if len(usedMetrics) < 10 || len(usedEvents) < 5 {
		t.Errorf("dashboard uses %d metrics and %d events, expected more", len(usedMetrics), len(usedEvents))
	}
}

func TestReadmeDocumentsTheInterface(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	for _, info := range obs.Catalog {
		if !strings.Contains(readme, "`"+info.Name+"`") {
			t.Errorf("README does not document metric %s", info.Name)
		}
	}
	for _, event := range obs.Events {
		if !strings.Contains(readme, "`"+event+"`") {
			t.Errorf("README does not document event %s", event)
		}
	}
	for _, variable := range []string{
		"PUSHOVER_TOKEN", "PUSHOVER_USER", "PUSHOVER_DEVICE",
		"BASEALERT_CONFIG", "BASEALERT_STATE", "BASEALERT_HTTP_ADDR", "BASEALERT_LOG_LEVEL", "BASEALERT_LOG_FORMAT",
	} {
		if !strings.Contains(readme, "`"+variable+"`") {
			t.Errorf("README does not document %s", variable)
		}
	}

	// The README must not mention metrics or events that do not exist.
	known := knownSeries()
	for _, name := range metricName.FindAllString(readme, -1) {
		if !known[name] && name != "basealert_" {
			t.Errorf("README mentions unknown metric %s", name)
		}
	}
	for _, match := range eventFilter.FindAllStringSubmatch(readme, -1) {
		for _, event := range strings.Split(match[1], "|") {
			if !slices.Contains(obs.Events, event) {
				t.Errorf("README filters on unknown event %q", event)
			}
		}
	}
}

type workflowFile struct {
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		Needs       string            `yaml:"needs"`
		Permissions map[string]string `yaml:"permissions"`
		Steps       []struct {
			Name string         `yaml:"name"`
			Uses string         `yaml:"uses"`
			If   string         `yaml:"if"`
			With map[string]any `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// The workflow publishes an image with the repository's token. These are the
// properties that keep that safe; a well-meant edit must not lose them.
func TestWorkflowIsPinnedAndLeastPrivilege(t *testing.T) {
	raw := readRepoFile(t, ".github/workflows/ci.yml")

	// A tag like v7 can be moved to other code. A commit cannot.
	pinned := regexp.MustCompile(`^\s*(- )?uses: [\w.-]+/[\w.-]+@[0-9a-f]{40} # v\d+\.\d+\.\d+$`)
	actions := 0
	for _, line := range strings.Split(raw, "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		actions++
		if !pinned.MatchString(line) {
			t.Errorf("action is not pinned to a commit with its version as a comment: %s", strings.TrimSpace(line))
		}
	}
	if actions < 6 {
		t.Errorf("found only %d actions, the workflow structure has changed", actions)
	}
	if strings.Contains(raw, "pull_request_target") {
		t.Error("pull_request_target would run code from forks with write access")
	}

	var wf workflowFile
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("workflow is not valid YAML: %v", err)
	}
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("default permissions = %v, want only contents: read", wf.Permissions)
	}
	for name, job := range wf.Jobs {
		for permission, level := range job.Permissions {
			if level != "read" && (name != "image" || permission != "packages") {
				t.Errorf("job %s has %s: %s", name, permission, level)
			}
		}
	}

	image, ok := wf.Jobs["image"]
	if !ok || image.Needs != "test" {
		t.Fatalf("the image job must exist and wait for the tests, needs = %q", image.Needs)
	}
	// Only pushes publish. Pull requests build without ever logging in.
	const onPush = "github.event_name == 'push'"
	logins, publishes := 0, 0
	for _, step := range image.Steps {
		if strings.HasPrefix(step.Uses, "docker/login-action@") {
			logins++
			if step.If != onPush {
				t.Errorf("registry login runs on %q, want %q", step.If, onPush)
			}
		}
		if push, ok := step.With["push"]; ok {
			publishes++
			if push != "${{ "+onPush+" }}" {
				t.Errorf("step %q pushes on %v", step.Name, push)
			}
			if step.With["platforms"] != "linux/amd64,linux/arm64" {
				t.Errorf("step %q builds for %v", step.Name, step.With["platforms"])
			}
		}
	}
	if logins != 1 || publishes != 1 {
		t.Errorf("found %d login and %d publishing steps, want one of each", logins, publishes)
	}
}

// The workflow reads the Go version and the build tags from the Dockerfile
// with sed. This test fails if the Dockerfile no longer has those lines in
// the shape the workflow expects.
func TestWorkflowCanReadTheDockerfile(t *testing.T) {
	dockerfile := readRepoFile(t, "Dockerfile")
	workflow := readRepoFile(t, ".github/workflows/ci.yml")
	for _, expression := range []string{`s/^FROM .*golang:\([0-9.]*\)-alpine.*/\1/p`, `s/^ARG GO_TAGS=//p`} {
		if !strings.Contains(workflow, expression) {
			t.Errorf("workflow no longer uses %s; update this test with it", expression)
		}
	}

	image := regexp.MustCompile(`(?m)^FROM .*golang:([0-9]+)\.([0-9]+)\.[0-9]+-alpine`).FindStringSubmatch(dockerfile)
	if image == nil {
		t.Fatal("Dockerfile has no line \"FROM ... golang:<major>.<minor>.<patch>-alpine\"")
	}
	tags := regexp.MustCompile(`(?m)^ARG GO_TAGS=(\S+)$`).FindStringSubmatch(dockerfile)
	if tags == nil {
		t.Fatal("Dockerfile has no line \"ARG GO_TAGS=...\"")
	}
	if !strings.Contains(tags[1], "timetzdata") {
		t.Errorf("GO_TAGS = %s; without timetzdata the scratch image has no time zones", tags[1])
	}

	// The image's toolchain must be able to build what go.mod asks for.
	module := regexp.MustCompile(`(?m)^go ([0-9]+)\.([0-9]+)`).FindStringSubmatch(readRepoFile(t, "go.mod"))
	if module == nil {
		t.Fatal("go.mod has no go directive")
	}
	number := func(s string) int { n, _ := strconv.Atoi(s); return n }
	if number(module[1]) > number(image[1]) || (module[1] == image[1] && number(module[2]) > number(image[2])) {
		t.Errorf("go.mod requires Go %s.%s, the Dockerfile builds with %s.%s", module[1], module[2], image[1], image[2])
	}
}

func TestDependabotCoversWhatIsPinned(t *testing.T) {
	var cfg struct {
		Updates []struct {
			Ecosystem string `yaml:"package-ecosystem"`
		} `yaml:"updates"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, ".github/dependabot.yml")), &cfg); err != nil {
		t.Fatalf("dependabot.yml is not valid YAML: %v", err)
	}
	var ecosystems []string
	for _, u := range cfg.Updates {
		ecosystems = append(ecosystems, u.Ecosystem)
	}
	for _, want := range []string{"github-actions", "gomod", "docker"} {
		if !slices.Contains(ecosystems, want) {
			t.Errorf("dependabot does not update %s; pinned versions there would go stale", want)
		}
	}
}

// Nothing the build pulls in may float: a moving tag changes what is built
// without any change in this repository.
func TestVersionsArePinned(t *testing.T) {
	dockerfile := readRepoFile(t, "Dockerfile")
	workflow := readRepoFile(t, ".github/workflows/ci.yml")

	// Base images: exact release in the tag, content fixed by the digest.
	pinnedImage := regexp.MustCompile(`^FROM --platform=\$BUILDPLATFORM \S+:\d+\.\d+\.\d+-alpine\d+\.\d+@sha256:[0-9a-f]{64} AS \w+$`)
	stages := 0
	for _, line := range strings.Split(dockerfile, "\n") {
		switch {
		case strings.HasPrefix(line, "FROM scratch"):
			stages++
		case strings.HasPrefix(line, "FROM "):
			stages++
			if !pinnedImage.MatchString(line) {
				t.Errorf("base image is not pinned to an exact release and a digest: %s", line)
			}
		case strings.HasPrefix(line, "# syntax="):
			t.Errorf("the syntax directive pulls a floating Dockerfile frontend: %s", line)
		case regexp.MustCompile(`\b(apk add|apt-get install|apt install)\b`).MatchString(line):
			t.Errorf("installs whatever package version the distribution serves today: %s", strings.TrimSpace(line))
		}
	}
	if stages != 2 {
		t.Errorf("found %d build stages, want the build stage and scratch", stages)
	}

	// Runners: a concrete Ubuntu release, never "latest".
	runners := regexp.MustCompile(`(?m)^\s*runs-on: (.+)$`).FindAllStringSubmatch(workflow, -1)
	if len(runners) != 2 {
		t.Errorf("found %d runs-on lines, want one per job", len(runners))
	}
	for _, r := range runners {
		if !regexp.MustCompile(`^ubuntu-\d\d\.\d\d$`).MatchString(r[1]) {
			t.Errorf("runs-on: %s is not a concrete runner image", r[1])
		}
	}

	// Build tools: exact buildx release, BuildKit by release and digest.
	var wf workflowFile
	if err := yaml.Unmarshal([]byte(workflow), &wf); err != nil {
		t.Fatal(err)
	}
	builders := 0
	for _, step := range wf.Jobs["image"].Steps {
		if !strings.HasPrefix(step.Uses, "docker/setup-buildx-action@") {
			continue
		}
		builders++
		if v, _ := step.With["version"].(string); !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(v) {
			t.Errorf("buildx version = %q, want an exact release", step.With["version"])
		}
		if v, _ := step.With["driver-opts"].(string); !regexp.MustCompile(`^image=moby/buildkit:v\d+\.\d+\.\d+@sha256:[0-9a-f]{64}$`).MatchString(v) {
			t.Errorf("BuildKit image = %q, want an exact release with its digest", step.With["driver-opts"])
		}
	}
	if builders != 1 {
		t.Errorf("found %d buildx setup steps, want 1", builders)
	}

	// The Go version handed to setup-go comes from the Dockerfile, so no
	// second version may be written into the workflow.
	if regexp.MustCompile(`go-version: ["']?\d`).MatchString(workflow) {
		t.Error("the workflow hard-codes a Go version; it must come from the Dockerfile")
	}
}
