// Package domain holds the API's core types and their JSON representation (docs/API.md).
package domain

import (
	"errors"
	"time"
)

// Role is a user's authorization level.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

var roleRank = map[Role]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}

// Valid reports whether r is a known role.
func (r Role) Valid() bool { _, ok := roleRank[r]; return ok }

// AtLeast reports whether r has at least the privileges of min.
func (r Role) AtLeast(min Role) bool { return roleRank[r] >= roleRank[min] }

// Sentinel errors returned by stores; translated to HTTP errors by the API layer.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Role         Role      `json:"role"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type CameraStatus struct {
	State     string  `json:"state"`
	FPS       float64 `json:"fps"`
	LatencyMS float64 `json:"latency_ms"`
	Message   string  `json:"message,omitempty"`
	UpdatedAt string  `json:"updated_at,omitempty"`
}

type Camera struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	SourceType  string       `json:"source_type"`
	SourceURI   string       `json:"source_uri"`
	Enabled     bool         `json:"enabled"`
	PrivacyMode bool         `json:"privacy_mode"`
	TargetFPS   float64      `json:"target_fps"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Status      CameraStatus `json:"status"`
}

type Point [2]float64

type Zone struct {
	ID        string    `json:"id"`
	CameraID  string    `json:"camera_id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Points    []Point   `json:"points"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
}

type RuleParams struct {
	Classes         []string `json:"classes,omitempty"`
	Seconds         *float64 `json:"seconds,omitempty"`
	MaxCount        *int     `json:"max_count,omitempty"`
	CooldownSeconds *float64 `json:"cooldown_seconds,omitempty"`
}

type Rule struct {
	ID             string     `json:"id"`
	CameraID       string     `json:"camera_id"`
	ZoneID         *string    `json:"zone_id"`
	Name           string     `json:"name"`
	Type           string     `json:"type"`
	Params         RuleParams `json:"params"`
	Severity       string     `json:"severity"`
	Enabled        bool       `json:"enabled"`
	NotifyEmail    bool       `json:"notify_email"`
	NotifyTelegram bool       `json:"notify_telegram"`
	CreatedAt      time.Time  `json:"created_at"`
}

type Incident struct {
	ID                  string    `json:"id"`
	CameraID            string    `json:"camera_id"`
	CameraName          string    `json:"camera_name"`
	RuleID              *string   `json:"rule_id"`
	RuleName            string    `json:"rule_name"`
	RuleType            string    `json:"rule_type"`
	Severity            string    `json:"severity"`
	Status              string    `json:"status"`
	OccurredAt          time.Time `json:"occurred_at"`
	Message             string    `json:"message"`
	ObjectClass         string    `json:"object_class"`
	TrackIDs            []int32   `json:"track_ids"`
	Description         *string   `json:"description"`
	DescriptionProvider *string   `json:"description_provider"`
	SnapshotURL         *string   `json:"snapshot_url"`
	ClipURL             *string   `json:"clip_url"`
	AcknowledgedBy      *string   `json:"acknowledged_by"`
	UpdatedAt           time.Time `json:"updated_at"`
	SnapshotPath        *string   `json:"-"`
	ClipPath            *string   `json:"-"`
	NotifyEmail         bool      `json:"-"`
	NotifyTelegram      bool      `json:"-"`
}

type Model struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Backend         string   `json:"backend"`
	Classes         []string `json:"classes"`
	Available       bool     `json:"available"`
	MAP50           *float64 `json:"map50"`
	MAP5095         *float64 `json:"map50_95"`
	LatencyMS       *float64 `json:"latency_ms"`
	FPS             *float64 `json:"fps"`
	BenchmarkSource *string  `json:"benchmark_source"`
	Active          bool     `json:"active"`
}

type Page[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// IncidentFilter are the search parameters of GET /incidents.
type IncidentFilter struct {
	Query    string
	CameraID string
	Severity string
	Status   string
	RuleType string
	From     *time.Time
	To       *time.Time
	Page     int
	PageSize int
}

type CountBucket struct {
	Bucket      time.Time `json:"bucket"`
	ObjectClass string    `json:"object_class"`
	AvgCount    float64   `json:"avg_count"`
	MaxCount    int       `json:"max_count"`
}

type Heatmap struct {
	Cols  int       `json:"cols"`
	Rows  int       `json:"rows"`
	Max   int       `json:"max"`
	Cells []int     `json:"cells"`
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
}

type CrossingBucket struct {
	Bucket    time.Time `json:"bucket"`
	ZoneID    string    `json:"zone_id"`
	ZoneName  string    `json:"zone_name"`
	Direction string    `json:"direction"`
	Count     int       `json:"count"`
}

type Summary struct {
	CamerasOnline   int `json:"cameras_online"`
	CamerasTotal    int `json:"cameras_total"`
	OpenIncidents   int `json:"open_incidents"`
	Incidents24h    int `json:"incidents_24h"`
	PeopleNow       int `json:"people_now"`
	VehiclesNow     int `json:"vehicles_now"`
	CrossingsIn24h  int `json:"crossings_in_24h"`
	CrossingsOut24h int `json:"crossings_out_24h"`
}

// VehicleClasses are aggregated as "vehicles" in summaries.
var VehicleClasses = []string{"car", "truck", "bus", "motorcycle", "vehicle"}
