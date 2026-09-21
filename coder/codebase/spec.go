package codebase

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/fallback"
	"gopkg.in/yaml.v3"
)

const maxSpecSize = 256 << 10

const defaultSpecFile = `---
version: 1
index:
  model: ""
  effort: default
  max_loop_times: 100
  max_output_tokens: 8192
context:
  model: ""
  effort: none
  max_loop_times: 12
  max_output_tokens: 4000
  timeout: 60s
schedule:
  idle_delay: 5m
  conversation_pairs: 10
  coverage_page_size: 500
---
Prefer the maintained knowledge base before inspecting the live repository.
Follow only the few knowledge routes relevant to the task. Verify live code or Git history only when knowledge is missing, stale, contradictory, or the question requires it.
Avoid broad exploration, keep outputs concise, and stop as soon as the available evidence is sufficient.
Remove or mark obsolete knowledge, merge duplicates, and state uncertainty instead of inventing facts.
`

type duration struct{ time.Duration }

func (d *duration) UnmarshalYAML(value *yaml.Node) error {
	v, err := time.ParseDuration(value.Value)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

type modeSpec struct {
	Model           string `yaml:"model"`
	Effort          string `yaml:"effort"`
	MaxLoopTimes    int    `yaml:"max_loop_times"`
	MaxOutputTokens int64  `yaml:"max_output_tokens"`
}

type contextSpec struct {
	modeSpec `yaml:",inline"`
	Timeout  duration `yaml:"timeout"`
}

type scheduleSpec struct {
	IdleDelay         duration `yaml:"idle_delay"`
	ConversationPairs int      `yaml:"conversation_pairs"`
	CoveragePageSize  int      `yaml:"coverage_page_size"`
}

type Spec struct {
	Version  int          `yaml:"version"`
	Index    modeSpec     `yaml:"index"`
	Context  contextSpec  `yaml:"context"`
	Schedule scheduleSpec `yaml:"schedule"`
	Body     string       `yaml:"-"`
}

func createDefaultSpec(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = io.WriteString(f, defaultSpecFile); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

func loadSpec(path string, pool *fallback.ModelPool) (Spec, error) {
	f, err := os.Open(path)
	if err != nil {
		return Spec{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Spec{}, err
	}
	if info.Size() > maxSpecSize {
		return Spec{}, fmt.Errorf("Codebase AGENT-SPEC exceeds %d bytes", maxSpecSize)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSpecSize+1))
	if err != nil {
		return Spec{}, err
	}
	if len(data) > maxSpecSize || !utf8.Valid(data) {
		return Spec{}, fmt.Errorf("invalid Codebase AGENT-SPEC encoding or size")
	}
	front, body, err := splitFrontmatter(data)
	if err != nil {
		return Spec{}, err
	}
	for _, line := range bytes.Split(front, []byte("\n")) {
		if bytes.Equal(bytes.TrimSpace(line), []byte("...")) {
			return Spec{}, fmt.Errorf("decode Codebase AGENT-SPEC: explicit YAML document boundaries are not allowed")
		}
	}
	var spec Spec
	dec := yaml.NewDecoder(bytes.NewReader(front))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return Spec{}, fmt.Errorf("decode Codebase AGENT-SPEC: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple YAML documents")
		}
		return Spec{}, fmt.Errorf("decode Codebase AGENT-SPEC: %w", err)
	}
	spec.Body = strings.TrimSpace(string(body))
	if err := validateSpec(spec, pool); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

func splitFrontmatter(data []byte) ([]byte, []byte, error) {
	const marker = "---\n"
	if !bytes.HasPrefix(data, []byte(marker)) {
		return nil, nil, fmt.Errorf("Codebase AGENT-SPEC requires YAML front matter")
	}
	rest := data[len(marker):]
	i := bytes.Index(rest, []byte("\n---\n"))
	if i < 0 {
		return nil, nil, fmt.Errorf("Codebase AGENT-SPEC has unterminated front matter")
	}
	return rest[:i], rest[i+len("\n---\n"):], nil
}

func validateSpec(spec Spec, pool *fallback.ModelPool) error {
	if spec.Version != 1 {
		return fmt.Errorf("unsupported Codebase AGENT-SPEC version %d", spec.Version)
	}
	if spec.Body == "" {
		return fmt.Errorf("Codebase AGENT-SPEC policy is empty")
	}
	models := map[string]bool{}
	if pool != nil {
		client := pool.NewClient(fallback.NewSessionPolicy(providers.ClientPolicy{}), providers.ClientPolicy{})
		for _, entry := range client.Entries() {
			models[entry.Name] = true
		}
	}
	for name, mode := range map[string]modeSpec{"index": spec.Index, "context": spec.Context.modeSpec} {
		if mode.Model != "" && !models[mode.Model] {
			return fmt.Errorf("%s selects unknown model %q", name, mode.Model)
		}
		if !providers.IsValidReasoningEffort(mode.Effort) {
			return fmt.Errorf("%s has invalid effort %q", name, mode.Effort)
		}
		if mode.MaxLoopTimes < 1 || mode.MaxLoopTimes > 500 {
			return fmt.Errorf("%s max_loop_times must be 1-500", name)
		}
		if mode.MaxOutputTokens < 256 || mode.MaxOutputTokens > 65536 {
			return fmt.Errorf("%s max_output_tokens must be 256-65536", name)
		}
	}
	if spec.Context.Timeout.Duration < time.Second || spec.Context.Timeout.Duration > 10*time.Minute {
		return fmt.Errorf("context timeout must be 1s-10m")
	}
	if spec.Schedule.IdleDelay.Duration < time.Minute || spec.Schedule.IdleDelay.Duration > 24*time.Hour {
		return fmt.Errorf("idle_delay must be 1m-24h")
	}
	if spec.Schedule.ConversationPairs < 1 || spec.Schedule.ConversationPairs > 100 {
		return fmt.Errorf("conversation_pairs must be 1-100")
	}
	if spec.Schedule.CoveragePageSize < 1 || spec.Schedule.CoveragePageSize > 10000 {
		return fmt.Errorf("coverage_page_size must be 1-10000")
	}
	return nil
}
