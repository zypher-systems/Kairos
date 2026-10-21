// Package ascode is the jobs-as-code surface: canonical YAML for jobs,
// workflows, secret stores, and calendars, export, apply (dry-run + upsert by name), and git clone.
package ascode

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const APIVersion = "kairos/v1"

const (
	KindJob         = "Job"
	KindWorkflow    = "Workflow"
	KindSecretStore = "SecretStore"
	KindCalendar    = "Calendar"
)

// Document is one YAML object.
type Document struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   Meta   `yaml:"metadata"`
	Spec       Spec   `yaml:"spec"`
}

// Meta is name and labels shared by kinds.
type Meta struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	Enabled     *bool    `yaml:"enabled,omitempty"`
}

// Spec holds job or workflow fields. Unused fields stay empty.
type Spec struct {
	Schedule               *ScheduleSpec     `yaml:"schedule,omitempty"`
	Target                 *TargetSpec       `yaml:"target,omitempty"`
	Exec                   *ExecSpec         `yaml:"exec,omitempty"`
	Env                    map[string]string `yaml:"env,omitempty"`
	Secrets                *SecretSpec       `yaml:"secrets,omitempty"`
	Retry                  *RetrySpec        `yaml:"retry,omitempty"`
	TimeoutSeconds         int               `yaml:"timeout_seconds,omitempty"`
	SLAMissedGraceSeconds  int               `yaml:"sla_missed_grace_seconds,omitempty"`
	SLALateSeconds         int               `yaml:"sla_late_seconds,omitempty"`
	RequireApproval        bool              `yaml:"require_approval,omitempty"`
	ApprovalTimeoutSeconds int               `yaml:"approval_timeout_seconds,omitempty"`
	Concurrency            string            `yaml:"concurrency,omitempty"`
	Notify                 []string          `yaml:"notify,omitempty"`
	Watch                  *WatchSpec        `yaml:"watch,omitempty"`
	Parameters             map[string]string `yaml:"parameters,omitempty"`
	Edges                  []EdgeSpec        `yaml:"edges,omitempty"`
	SecretRefs             *[]SecretRefSpec  `yaml:"secret_refs,omitempty"`
	Calendar               *string           `yaml:"calendar,omitempty"`
	Dates                  []string          `yaml:"dates,omitempty"`
	Type                   string            `yaml:"type,omitempty"`
	Address                string            `yaml:"address,omitempty"`
	Mount                  string            `yaml:"mount,omitempty"`
	Namespace              string            `yaml:"namespace,omitempty"`
	Token                  string            `yaml:"token,omitempty"`
}

type ScheduleSpec struct {
	Type            string `yaml:"type"`
	Cron            string `yaml:"cron,omitempty"`
	IntervalSeconds int    `yaml:"interval_seconds,omitempty"`
	At              string `yaml:"at,omitempty"`
	Timezone        string `yaml:"timezone,omitempty"`
}

type TargetSpec struct {
	Type   string            `yaml:"type"`
	Host   string            `yaml:"host,omitempty"`
	Agent  string            `yaml:"agent,omitempty"`
	Labels map[string]string `yaml:"labels,omitempty"`
}

type ExecSpec struct {
	Mode     string        `yaml:"mode"`
	Script   string        `yaml:"script,omitempty"`
	Image    string        `yaml:"image,omitempty"`
	Command  []string      `yaml:"command,omitempty"`
	Workdir  string        `yaml:"workdir,omitempty"`
	Transfer *TransferSpec `yaml:"transfer,omitempty"`
}

type TransferSpec struct {
	Source         EndpointSpec `yaml:"source"`
	Dest           EndpointSpec `yaml:"dest"`
	Overwrite      bool         `yaml:"overwrite,omitempty"`
	DeleteSource   bool         `yaml:"delete_source,omitempty"`
	PreserveMtime  bool         `yaml:"preserve_mtime,omitempty"`
	VerifyChecksum bool         `yaml:"verify_checksum,omitempty"`
	// FromTrigger copies the files the job's file trigger matched.
	FromTrigger bool `yaml:"from_trigger,omitempty"`
}

// EndpointSpec names one side of a transfer: a storage location by name
// (kind location), the library, or a legacy ssh host / agent path.
type EndpointSpec struct {
	Kind     string `yaml:"kind"`
	Location string `yaml:"location,omitempty"`
	File     string `yaml:"file,omitempty"`
	Host     string `yaml:"host,omitempty"`
	Agent    string `yaml:"agent,omitempty"`
	Path     string `yaml:"path,omitempty"`
}

type RetrySpec struct {
	Count       int `yaml:"count,omitempty"`
	BaseDelayMs int `yaml:"base_delay_ms,omitempty"`
	MaxDelayMs  int `yaml:"max_delay_ms,omitempty"`
}

type WatchSpec struct {
	Enabled         bool   `yaml:"enabled"`
	Target          string `yaml:"target,omitempty"` // local | ssh | agent | location
	Host            string `yaml:"host,omitempty"`
	Agent           string `yaml:"agent,omitempty"`
	Location        string `yaml:"location,omitempty"`
	Path            string `yaml:"path,omitempty"`
	IntervalSeconds int    `yaml:"interval_seconds,omitempty"`
}

type EdgeSpec struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
	When string `yaml:"when,omitempty"`
}

type SecretRefSpec struct {
	Name  string `yaml:"name"`
	Store string `yaml:"store"`
	Path  string `yaml:"path"`
	Key   string `yaml:"key"`
}

// SecretSpec is a list of names (export) or a map of values (apply).
type SecretSpec struct {
	Names  []string
	Values map[string]string
}

func (s SecretSpec) MarshalYAML() (any, error) {
	if len(s.Values) > 0 {
		return s.Values, nil
	}
	if s.Names == nil {
		return []string{}, nil
	}
	return s.Names, nil
}

func (s *SecretSpec) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Tag == "!!null" {
		return nil
	}
	switch value.Kind {
	case yaml.SequenceNode:
		return value.Decode(&s.Names)
	case yaml.MappingNode:
		s.Values = map[string]string{}
		return value.Decode(&s.Values)
	case yaml.ScalarNode:
		if value.Value == "" {
			return nil
		}
	}
	return fmt.Errorf("secrets must be a list of names or a map of values")
}

func (s *SecretSpec) names() []string {
	if s == nil {
		return nil
	}
	if len(s.Values) > 0 {
		out := make([]string, 0, len(s.Values))
		for k := range s.Values {
			out = append(out, k)
		}
		return out
	}
	return s.Names
}

func (s *SecretSpec) valueMap() map[string]string {
	if s == nil {
		return map[string]string{}
	}
	if s.Values != nil {
		return s.Values
	}
	out := map[string]string{}
	for _, n := range s.Names {
		out[n] = ""
	}
	return out
}

// Parse reads one or more YAML documents.
func Parse(r io.Reader) ([]Document, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return ParseBytes(raw)
}

// ParseBytes splits a multi-document YAML stream.
func ParseBytes(raw []byte) ([]Document, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var out []Document
	for i := 1; ; i++ {
		var doc Document
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("yaml document %d: %w", i, err)
		}
		if doc.Kind == "" && doc.Metadata.Name == "" {
			continue
		}
		if doc.APIVersion == "" {
			doc.APIVersion = APIVersion
		}
		if doc.APIVersion != APIVersion {
			return nil, fmt.Errorf("yaml document %d: unsupported apiVersion %q", i, doc.APIVersion)
		}
		switch doc.Kind {
		case KindJob, KindWorkflow, KindSecretStore, KindCalendar:
		default:
			return nil, fmt.Errorf("yaml document %d: kind must be Job, Workflow, SecretStore, or Calendar", i)
		}
		if strings.TrimSpace(doc.Metadata.Name) == "" {
			return nil, fmt.Errorf("yaml document %d: metadata.name is required", i)
		}
		out = append(out, doc)
	}
	return out, nil
}

// MarshalDocs writes documents separated by ---.
func MarshalDocs(docs []Document) ([]byte, error) {
	var buf bytes.Buffer
	for i, d := range docs {
		if i > 0 {
			buf.WriteString("---\n")
		}
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(d); err != nil {
			_ = enc.Close()
			return nil, err
		}
		_ = enc.Close()
	}
	return buf.Bytes(), nil
}

func boolPtr(v bool) *bool { return &v }

func enabledOf(m Meta, fallback bool) bool {
	if m.Enabled == nil {
		return fallback
	}
	return *m.Enabled
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp %q (want RFC3339)", s)
	}
	return &t, nil
}
