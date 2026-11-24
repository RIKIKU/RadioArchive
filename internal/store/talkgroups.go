package store

import "slices"

// The names a talkgroup, tag, group or system may give its alert and LED
// colour (SPEC.md R22, section 13; ADR 0006). Migration 00007's checks list
// the same names. They're names, not values: the colours become ArchiveUx
// tokens and the sounds ship with the frontend.
//
// A setting left unset (NULL) is taken from the talkgroup's tag, then its
// groups, then its system. NoSetting stops there: no alert, or no colour.
var (
	Alerts     = []string{NoSetting, "beep", "chime", "pip", "tone"}
	LEDColours = []string{NoSetting, "red", "orange", "amber", "green", "teal", "blue", "purple", "pink"}
)

// NoSetting is an alert or LED colour of "none", which isn't taken from
// anywhere else.
const NoSetting = "none"

// IsAlert says whether s names an alert.
func IsAlert(s string) bool { return slices.Contains(Alerts, s) }

// IsLEDColour says whether s names an LED colour.
func IsLEDColour(s string) bool { return slices.Contains(LEDColours, s) }

// MaxDelayMinutes is the longest delay a talkgroup may have (R23): a day.
const MaxDelayMinutes = 1440
