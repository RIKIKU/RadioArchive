package telemetry

// The allowlist: every metric the API exports (SPEC.md R90; ADR 0040).
// The alert rules and dashboards (deploy/monitoring) use these names.
var (
	BuildInfo = &Desc{Name: "radioarchive_build_info", Type: Gauge, Labels: []string{"version"},
		Help: "Always 1: the API's version."}

	// Recorders and their signals (R6, R14), one series per active
	// recorder, named as admins named it.
	RecorderConnected = &Desc{Name: "radioarchive_recorder_connected", Type: Gauge, Labels: []string{"recorder"},
		Help: "1 while the recorder's client has its live connection open."}
	RecorderSignal = &Desc{Name: "radioarchive_recorder_signal", Type: Gauge, Labels: []string{"recorder", "signal"},
		Help: "1 while a recorder's signal is tripped: disconnected, deaf (or down) or quiet (R14)."}
	RecorderDeaf = &Desc{Name: "radioarchive_recorder_deaf", Type: Gauge, Labels: []string{"recorder", "reason"},
		Help: "1 while a recorder is deaf, by why: no_heartbeat (trunk-recorder down) or decode_rate (a control channel lost)."}
	RecorderQuietAfter = &Desc{Name: "radioarchive_recorder_quiet_after_seconds", Type: Gauge, Labels: []string{"recorder"},
		Help: "A recorder's quiet threshold, where an admin set one (R14)."}
	RecorderLastCall = &Desc{Name: "radioarchive_recorder_last_call_timestamp_seconds", Type: Gauge, Labels: []string{"recorder"},
		Help: "When the recorder's last call started."}
	RecorderLastConnected = &Desc{Name: "radioarchive_recorder_last_connected_timestamp_seconds", Type: Gauge, Labels: []string{"recorder"},
		Help: "When the recorder's client last connected."}
	RecorderClient = &Desc{Name: "radioarchive_recorder_client_info", Type: Gauge, Labels: []string{"recorder", "version"},
		Help: "Always 1: the version the recorder's client last connected with."}
	RecorderOutOfDate = &Desc{Name: "radioarchive_recorder_client_out_of_date", Type: Gauge, Labels: []string{"recorder"},
		Help: "1 when the recorder's client is a minor version behind the server (R6)."}
	RecorderDecodeRate = &Desc{Name: "radioarchive_recorder_decode_rate", Type: Gauge, Labels: []string{"recorder", "short_name"},
		Help: "Control-channel messages a second, per system the recorder's plugin reports (R14)."}
	RecorderQueueEntries = &Desc{Name: "radioarchive_recorder_queue_entries", Type: Gauge, Labels: []string{"recorder"},
		Help: "Recordings in the recorder client's queue, not yet uploaded (R5), from its last heartbeat."}
	RecorderQueueOldest = &Desc{Name: "radioarchive_recorder_queue_oldest_age_seconds", Type: Gauge, Labels: []string{"recorder"},
		Help: "How old the oldest recording in the recorder client's queue is (R5), from its last heartbeat."}
	RecorderLastHeartbeat = &Desc{Name: "radioarchive_recorder_last_heartbeat_timestamp_seconds", Type: Gauge, Labels: []string{"recorder"},
		Help: "When the recorder's client last sent a heartbeat on this connection."}
	SignalsTripped = &Desc{Name: "radioarchive_recorder_signals_tripped_total", Type: Counter, Labels: []string{"signal"},
		Help: "Times a recorder's signal went from clear to tripped."}
	RecorderNotice = &Desc{Name: "radioarchive_recorder_notice_active", Type: Gauge,
		Help: "1 while listeners see \"Some talkgroups aren't being received right now\" (R15)."}
	ClientsTooOld = &Desc{Name: "radioarchive_recorder_clients_too_old_total", Type: Counter,
		Help: "Recorder client connections and uploads refused for being more than one minor version behind (R6)."}
	ClientsOutOfDate = &Desc{Name: "radioarchive_recorder_clients_out_of_date_total", Type: Counter,
		Help: "Live connections opened by a recorder client one minor version behind (R6)."}
	RecorderErrors = &Desc{Name: "radioarchive_recorder_protocol_events_total", Type: Counter, Labels: []string{"kind"},
		Help: "Recorder protocol events: frames for unstarted calls, unknown types, sockets closed for bad input or a revoked key, bad heartbeat systems."}
	Uploads = &Desc{Name: "radioarchive_recorder_uploads_total", Type: Counter, Labels: []string{"status"},
		Help: "Recording uploads, by their answer."}

	// Calls.
	CallsRejected = &Desc{Name: "radioarchive_calls_rejected_total", Type: Counter, Labels: []string{"reason"},
		Help: "Calls refused since the API started (R12, R17, R18): not_attached, unknown_talkgroup or ignored_talkgroup."}
	FramesRefused = &Desc{Name: "radioarchive_live_frames_refused_total", Type: Counter,
		Help: "Live frames of refused calls (R12, R17, R18)."}
	RejectedUnattached = &Desc{Name: "radioarchive_rejected_unattached_calls", Type: Gauge, Labels: []string{"recorder", "short_name"},
		Help: "Calls rejected from a recorder and shortName not attached to a system, over the retention period (R12)."}
	RejectedTalkgroup = &Desc{Name: "radioarchive_rejected_talkgroup_calls", Type: Gauge, Labels: []string{"system", "talkgroup", "reason"},
		Help: "Calls rejected on an unknown or ignored talkgroup, over the retention period (R17, R18)."}
	RecordingsMissing = &Desc{Name: "radioarchive_recordings_missing", Type: Gauge,
		Help: "Calls that ended between 10 minutes and 24 hours ago whose recording hasn't arrived (R19)."}
	OverlappingCalls = &Desc{Name: "radioarchive_overlapping_calls", Type: Gauge, Labels: []string{"system", "talkgroup", "recorder", "other_recorder"},
		Help: "Overlapping calls from two recorders on one system and talkgroup in the last 24 hours (R13)."}

	// The live feed (R36-R38).
	LiveDelay = &Desc{Name: "radioarchive_live_delay_seconds", Type: Histogram,
		Buckets: []float64{0.1, 0.25, 0.5, 0.75, 1, 1.5, 2, 3, 5, 10, 30},
		Help:    "From a live call's first PCM at the recorder to the server passing its first frame to listeners (R38)."}
	LiveFrames = &Desc{Name: "radioarchive_live_frames_total", Type: Counter, Labels: []string{"result"},
		Help: "Live frames queued for listeners (sent), or dropped from a slow listener's full queue (dropped)."}
	LiveEvents = &Desc{Name: "radioarchive_live_events_total", Type: Counter, Labels: []string{"kind"},
		Help: "Live feed events: slow listeners closed, calls withdrawn (R31), delayed calls released and recovered (R23), failed checks."}
	LiveSockets = &Desc{Name: "radioarchive_live_sockets", Type: Gauge,
		Help: "Listener WebSockets open (R71). Admins let in over a cap aren't counted."}
	LiveSocketsLimit = &Desc{Name: "radioarchive_live_sockets_limit", Type: Gauge,
		Help: "LIMIT_LIVE_TOTAL: listener WebSockets open at once in all (R71)."}
	LiveFull = &Desc{Name: "radioarchive_live_full", Type: Gauge,
		Help: "1 while every listener WebSocket the radio allows is open: new listeners see \"The radio is full\" (R71)."}
	ZipsOpen = &Desc{Name: "radioarchive_zips_open", Type: Gauge,
		Help: "Zips being sent (R72)."}
	LimitHits = &Desc{Name: "radioarchive_limit_hits_total", Type: Counter, Labels: []string{"limit"},
		Help: "Requests refused over a heavy-use limit (R70-R73): requests, playback, live or zip."}

	// The recordings volume and retention (R88).
	DiskBytes = &Desc{Name: "radioarchive_recordings_volume_bytes", Type: Gauge,
		Help: "The recordings volume's size."}
	DiskFree = &Desc{Name: "radioarchive_recordings_volume_free_bytes", Type: Gauge,
		Help: "Space left on the recordings volume."}
	DiskState = &Desc{Name: "radioarchive_recordings_volume_state", Type: Gauge, Labels: []string{"state"},
		Help: "1 for the volume's state: ok, warning (under 15 % free), low (retention being shortened) or full (R88)."}
	DiskMinFree = &Desc{Name: "radioarchive_recordings_volume_min_free_ratio", Type: Gauge,
		Help: "RECORDINGS_MIN_FREE, as a fraction."}
	Retention = &Desc{Name: "radioarchive_retention_deleted_total", Type: Counter, Labels: []string{"kind"},
		Help: "Recordings deleted: expired (the nightly run), orphans (files with no row), shortened (early, for low disk)."}
	VolumeFull = &Desc{Name: "radioarchive_recordings_volume_full_total", Type: Counter,
		Help: "Times \"recordings volume full\" was raised (R88)."}
	NightlyLast = &Desc{Name: "radioarchive_nightly_last_run_timestamp_seconds", Type: Gauge, Labels: []string{"status"},
		Help: "When the nightly run last finished, ok or failed."}

	// Backups (R89; ADR 0041), from the backup service's status file.
	BackupFailing = &Desc{Name: "radioarchive_backup_failing", Type: Gauge,
		Help: "1 when the last backup failed, or the last good one is more than 36 hours old (R89)."}
	BackupLastGood = &Desc{Name: "radioarchive_backup_last_good_timestamp_seconds", Type: Gauge,
		Help: "When the last good backup was made."}
	BackupBytes = &Desc{Name: "radioarchive_backup_last_good_bytes", Type: Gauge,
		Help: "The size of the last good backup's dumps."}
	BackupNights = &Desc{Name: "radioarchive_backup_nights", Type: Gauge,
		Help: "Nights of backups kept in the backup folder."}

	// The process.
	MemoryBytes = &Desc{Name: "radioarchive_process_memory_bytes", Type: Gauge, Labels: []string{"kind"},
		Help: "The Go runtime's memory: heap in use, and everything it holds from the system (sys)."}
	Goroutines = &Desc{Name: "radioarchive_process_goroutines", Type: Gauge,
		Help: "Goroutines running."}
	StartTime = &Desc{Name: "radioarchive_process_start_time_seconds", Type: Gauge,
		Help: "When the API started."}
)

// All is the allowlist, in the order /metrics writes it. Later slices add
// to it (backups, upgrades).
var All = []*Desc{
	BuildInfo,
	RecorderConnected, RecorderSignal, RecorderDeaf, RecorderQuietAfter, RecorderLastCall, RecorderLastConnected,
	RecorderClient, RecorderOutOfDate, RecorderDecodeRate, RecorderQueueEntries, RecorderQueueOldest,
	RecorderLastHeartbeat, SignalsTripped, RecorderNotice, ClientsTooOld, ClientsOutOfDate, RecorderErrors, Uploads,
	CallsRejected, FramesRefused, RejectedUnattached, RejectedTalkgroup, RecordingsMissing, OverlappingCalls,
	LiveDelay, LiveFrames, LiveEvents, LiveSockets, LiveSocketsLimit, LiveFull, ZipsOpen, LimitHits,
	DiskBytes, DiskFree, DiskState, DiskMinFree, Retention, VolumeFull, NightlyLast,
	BackupFailing, BackupLastGood, BackupBytes, BackupNights,
	MemoryBytes, Goroutines, StartTime,
}
