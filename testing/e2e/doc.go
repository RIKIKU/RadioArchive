// Package e2e runs the audio path end to end: the fake recorder, then the
// recorder client (with libopus), then the server, then PostgreSQL, over
// real UDP, WebSocket and HTTP. Its tests need cgo, libopus and the test
// database (RADIOARCHIVE_TEST_DATABASE_URL), as in CI.
package e2e
