package model

import (
	"io"
	"time"
)

type Event struct {
	ID                string         `json:"event_id"`
	Type              string         `json:"event_type"`
	Timestamp         time.Time      `json:"timestamp"`
	ProjectID         string         `json:"project_id,omitempty"`
	TaskID            string         `json:"task_id,omitempty"`
	ActorAgentID      string         `json:"actor_agent_id,omitempty"`
	SessionID         string         `json:"session_id,omitempty"`
	AggregateRevision int64          `json:"aggregate_revision"`
	Data              map[string]any `json:"data"`
}

type ArtifactInput struct {
	ID               string
	ProjectID        string
	Kind             string
	ClaimedDigest    string
	SourceCommit     string
	TaskIDs          []string
	ProducerSession  string
	VerificationRefs []string
	Data             io.Reader
}

type Artifact struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	Kind             string    `json:"kind"`
	Digest           string    `json:"digest"`
	SourceCommit     string    `json:"source_commit"`
	TaskIDs          []string  `json:"task_ids"`
	ProducerSession  string    `json:"producer_session,omitempty"`
	VerificationRefs []string  `json:"verification_refs"`
	Path             string    `json:"path"`
	Size             int64     `json:"size_bytes"`
	CreatedAt        time.Time `json:"created_at"`
}

type Verification struct {
	ID            string
	TaskID        string
	SessionID     string
	Commit        string
	Command       []string
	ExitStatus    int
	OutputDigest  string
	Valid         bool
	CreatedAt     time.Time
	InvalidatedAt *time.Time
}

type WorkerRun struct {
	ID               string
	TaskID           string
	SessionID        string
	Adapter          string
	AdapterVersion   string
	BaseCommit       string
	ResultCommit     string
	StartedAt        time.Time
	EndedAt          *time.Time
	ExitStatus       *int
	Status           string
	StdoutArtifactID string
	StderrArtifactID string
	Verification     []string
	Revision         int64
}

type RunFinish struct {
	ID               string
	Status           string
	ResultCommit     string
	EndedAt          time.Time
	ExitStatus       *int
	StdoutArtifactID string
	StderrArtifactID string
	Verification     []string
	ExpectedRevision int64
}
