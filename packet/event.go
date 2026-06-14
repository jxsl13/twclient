package packet

// MapInfo holds the metadata received in the MAP_CHANGE message.
type MapInfo struct {
	Name   string
	CRC    int
	Size   int
	Sha256 [32]byte // DDNet extension; zero if not provided
	// NumChunksPerRequest is how many MAP_DATA chunks the 0.7 server sends per
	// REQUEST_MAP_DATA (sv_map_window). The client requests one window, receives
	// exactly this many chunks, then requests the next — flooding requests
	// desyncs the server (B12). 0 if not advertised (older / 0.6).
	NumChunksPerRequest int
}

// Event is a high-level occurrence delivered by a Session.
// Use a type switch to inspect the concrete type.
type Event interface {
	eventTag()
}

// EventSnapshot is delivered when a new game snapshot has been fully
// delta-decompressed. Items contain absolute field values.
type EventSnapshot struct {
	Snap *Snapshot
}

func (EventSnapshot) eventTag() {}

// EventMapChange is delivered when the server sends a new MAP_CHANGE.
type EventMapChange struct {
	Info MapInfo
}

func (EventMapChange) eventTag() {}

// EventClose is delivered when the server sends a disconnect.
type EventClose struct {
	Reason string
}

func (EventClose) eventTag() {}

// EventRaceFinish is delivered when a race finish is detected.
// In 0.6 this comes from the DDRaceTime legacy message (with Finish=true).
// In 0.7 this comes from the SV_RACE_FINISH game message.
//
// Refs:
//   - DDNet src/game/client/components/race_demo.cpp (0.6 DDRaceTime)
//   - DDNet src/engine/shared/protocol7.h (0.7 NETMSG_SV_RACE_FINISH)
type EventRaceFinish struct {
	TimeCentis int  // Race time in centiseconds
	Finish     bool // True if race completed (always true for 0.7)
}

func (EventRaceFinish) eventTag() {}

// EventCheckpoint is delivered when crossing a race checkpoint.
// In 0.6 this comes from the DDRaceTime legacy message (CheckCentis field).
// In 0.7 this comes from the SV_CHECKPOINT game message.
//
// Refs:
//   - DDNet src/game/client/components/race_demo.cpp (0.6 DDRaceTime)
//   - DDNet src/engine/shared/protocol7.h (0.7 NETMSG_SV_CHECKPOINT)
type EventCheckpoint struct {
	DiffCentis int // Time difference at checkpoint (negative = faster)
}

func (EventCheckpoint) eventTag() {}

// EventRecord is delivered for Record legacy game messages (0.6 only).
type EventRecord struct {
	ServerBestCentis int
	PlayerBestCentis int
}

func (EventRecord) eventTag() {}

// EventInputTiming is delivered when the server responds to our INPUT
// with timing feedback. IntendedTick is the predTick we sent; TimeLeft
// is how many milliseconds remained before the server processed that tick.
//
// Refs:
//   - DDNet src/engine/shared/protocol.h (NETMSG_INPUTTIMING)
type EventInputTiming struct {
	IntendedTick int
	TimeLeft     int
}

func (EventInputTiming) eventTag() {}

// SendEvent sends an event on the channel without blocking.
// If the channel is full, it drops the oldest event to make room.
// This prevents the reader goroutine from stalling when consumers
// are slow to drain events.
func SendEvent(ch chan Event, ev Event) {
	select {
	case ch <- ev:
	default:
		// Channel full — drop oldest event to make room
		select {
		case <-ch:
		default:
		}
		// Retry send (still non-blocking in case of race)
		select {
		case ch <- ev:
		default:
		}
	}
}
