// Package localapi is the local API that runs on every machine: the HTTP server a game server
// calls at GS_LOCAL_API_URL (ready, allocation long-poll, shutdown notice, players, labels, self-stop, frame times),
// the GS_* environment variables, and the allocation file. The contract is openapi.yaml in this folder.
//
// The machine agent runs one Server per machine and registers each game server it starts. The same package is used
// by the local test agent, so a game behaves the same on a developer's PC as on our machines.
package localapi

import "time"

// Self is a game server's own record (GET /v0/self).
type Self struct {
	ID                  string            `json:"id"`
	Kind                string            `json:"kind"` // "match" or "persistent_server"
	App                 string            `json:"app"`
	Version             string            `json:"version"`
	Region              string            `json:"region"`
	Continent           string            `json:"continent"`
	PublicIP            string            `json:"public_ip"`
	Ports               map[string]Port   `json:"ports"`
	Labels              map[string]string `json:"labels"`
	Status              Status            `json:"status"`
	Players             Players           `json:"players"`
	AllocatedAt         *time.Time        `json:"allocated_at"`
	ShutdownRequestedAt *time.Time        `json:"shutdown_requested_at"`
}

// The two kinds of game server (Self.Kind, GS_KIND).
const (
	KindMatch            = "match"
	KindPersistentServer = "persistent_server"
	// KindWorld is the older name for a persistent server; treat it as KindPersistentServer (IsPersistentServer).
	KindWorld = "world"
)

// IsPersistentServer reports whether a kind (Self.Kind, GS_KIND) is a persistent server, under its current name or
// its older one (world).
func IsPersistentServer(kind string) bool { return kind == KindPersistentServer || kind == KindWorld }

// ValidKind reports whether a kind is one a game server can have: match, persistent_server or world.
func ValidKind(kind string) bool { return kind == KindMatch || IsPersistentServer(kind) }

// continents are the values of Self.Continent and GS_CONTINENT. Africa and the Middle East are here so a later
// continent is never taken for a region.
var continents = map[string]bool{
	"north-america": true, "south-america": true, "europe": true, "asia": true, "oceania": true,
	"africa": true, "middle-east": true,
}

// IsContinent reports whether a value names a continent (north-america) rather than a region (us-central).
func IsContinent(v string) bool { return continents[v] }

// Port is one named port. Internal is the container port on Linux (0 elsewhere, and for a Linux passthrough port,
// which listens on Port itself).
type Port struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // "udp" or "tcp"
	Internal int    `json:"internal,omitempty"`
}

// Status as the game server sees it.
type Status string

const (
	StatusStarting Status = "starting" // launched, not ready yet
	StatusWarm     Status = "warm"     // pre-started (a ready server), waiting to be allocated
	StatusRunning  Status = "running"  // ready and (for a ready server) allocated
	StatusStopping Status = "stopping" // a stop has been requested
)

// Older names a game may still be given or expect: active for running, and sleeping and waking for a persistent
// server's paused and resuming. Status.Normal maps them to the current names.
const (
	StatusActive Status = "active"
)

// Normal returns the status under its current name: active is running, sleeping is paused and waking is resuming.
// Any other value is returned as it is.
func (s Status) Normal() Status {
	switch s {
	case StatusActive:
		return StatusRunning
	case "sleeping":
		return "paused"
	case "waking":
		return "resuming"
	}
	return s
}

// IsRunning reports whether the status is running, under its current name or its older one (active).
func (s Status) IsRunning() bool { return s.Normal() == StatusRunning }

// Allocation is the match data (GET /v0/self/allocation and the allocation file).
type Allocation struct {
	GameServerID string            `json:"game_server_id"`
	AllocatedAt  time.Time         `json:"allocated_at"`
	ExternalID   string            `json:"external_id,omitempty"`
	Labels       map[string]string `json:"labels"`
	Env          map[string]string `json:"env"`
	Args         []string          `json:"args"`
	ExpiresAt    *time.Time        `json:"expires_at"`
	// SecretEnv holds the start request's secret values. They reach the game only in GET /v0/self/allocation's env;
	// they are never in the allocation file (it is on disk) and never encoded by this type.
	SecretEnv map[string]string `json:"-"`
}

// Shutdown is the shutdown notice (GET /v0/self/shutdown).
type Shutdown struct {
	RequestedAt time.Time `json:"requested_at"`
	Deadline    time.Time `json:"deadline"`
	Reason      string    `json:"reason"`
}

// Shutdown reasons the agent sends. Games are told to treat unknown ones like ReasonAPI.
const (
	ReasonAPI            = "api"
	ReasonBulk           = "bulk"
	ReasonSelf           = "self"
	ReasonTTL            = "ttl"
	ReasonMaxDuration    = "max_duration"
	ReasonBootTimeout    = "boot_timeout"
	ReasonHealthTimeout  = "health_timeout"
	ReasonRestart        = "restart"
	ReasonPaused         = "paused" // a persistent server stopping while nobody is on (pause when empty)
	ReasonOutOfDisk      = "out_of_disk"
	ReasonMachineFailure = "machine_failure"
	ReasonLostContact    = "lost_contact" // the machine lost contact with us and stops its persistent servers safely
)

// Players is what the game server last reported. IDs is nil when it reported only a count.
type Players struct {
	Count int      `json:"count"`
	IDs   []string `json:"ids,omitempty"`
	// Info is who they are and where, when the game says so. It is the game's latest word, kept only in memory on the
	// machine: positions change too often to record.
	Info []PlayerInfo `json:"players,omitempty"`
}

// PlayerInfo is one player as a game reports it. Only ID is needed.
type PlayerInfo struct {
	ID     string   `json:"id"`
	Name   string   `json:"name,omitempty"`
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	Z      *float64 `json:"z,omitempty"`
	Map    string   `json:"map,omitempty"`
	Team   string   `json:"team,omitempty"`
	PingMS *int     `json:"ping_ms,omitempty"`
}

// Tick is one second of frame times (POST /v0/self/tick).
type Tick struct {
	P50ms *float64 `json:"p50_ms,omitempty"`
	P99ms float64  `json:"p99_ms"`
	Hz    int      `json:"hz,omitempty"`
}

// EventType names what a game server told the agent.
type EventType string

const (
	EventReady   EventType = "ready"   // POST /v0/self/ready
	EventHealth  EventType = "health"  // POST /v0/self/health
	EventPlayers EventType = "players" // PUT /v0/self/players
	EventLabels  EventType = "labels"  // PATCH /v0/self/labels
	EventStop    EventType = "stop"    // POST /v0/self/stop (first call only)
	EventTick    EventType = "tick"    // POST /v0/self/tick
	EventSaved   EventType = "saved"   // POST /v0/self/saved
)

// Event is one call a game server made, handed to Options.OnEvent. Only the field for its type is set; Labels is
// the full set after the change.
type Event struct {
	GameServerID string
	Type         EventType
	At           time.Time
	Players      *Players
	Labels       map[string]string
	Reason       string
	Tick         *Tick
	Keep         string // saved: the name to keep these saves under, if any
}
