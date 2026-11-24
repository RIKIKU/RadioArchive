package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	cp "github.com/RIKIKU/RadioArchive/internal/clientproto"
	"github.com/RIKIKU/RadioArchive/internal/recordings"
	"github.com/RIKIKU/RadioArchive/internal/retention"
	"github.com/RIKIKU/RadioArchive/internal/store"
	"github.com/RIKIKU/RadioArchive/internal/store/db"
)

const incomingDir = retention.IncomingDir

// uploadError is an answer other than stored or duplicate.
type uploadError struct {
	code int
	resp cp.UploadResponse
}

func (e *uploadError) Error() string { return e.resp.Error }

func refuse(code int, errCode string) error {
	return &uploadError{code: code, resp: cp.UploadResponse{Error: errCode}}
}

// serveUpload stores a recording (ADR 0002, "Recordings: HTTPS upload").
// The file is written to a temporary name and checked against the
// metadata's size and SHA-256, then renamed into place and its row committed
// in one transaction, so a retry after a lost answer gets "duplicate".
func (rs *Recorders) serveUpload(w http.ResponseWriter, r *http.Request) {
	rec, ok := rs.authenticate(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, cp.MaxRecording+cp.MaxMetadata+1<<20)
	code, resp, err := rs.upload(r.Context(), rec, r)
	if err != nil {
		var ue *uploadError
		if errors.As(err, &ue) {
			code, resp = ue.code, ue.resp
		} else {
			rs.log.Error("recording upload failed", "recorder", rec.Name, "error", err)
			code, resp = http.StatusInternalServerError, cp.UploadResponse{Error: "internal"}
		}
	}
	status := resp.Status
	if status == "" {
		status = resp.Error
	}
	rs.Stat.upload(status)
	writeJSON(w, code, resp)
}

func (rs *Recorders) upload(ctx context.Context, rec store.Recorder, r *http.Request) (int, cp.UploadResponse, error) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "multipart/form-data" {
		return 0, cp.UploadResponse{}, refuse(http.StatusUnsupportedMediaType, "unsupported_media_type")
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return 0, cp.UploadResponse{}, refuse(http.StatusBadRequest, "malformed_multipart")
	}

	part, err := mr.NextPart()
	if err != nil || part.FormName() != "metadata" {
		return 0, cp.UploadResponse{}, refuse(http.StatusBadRequest, "metadata_first")
	}
	raw, err := io.ReadAll(io.LimitReader(part, cp.MaxMetadata+1))
	if err != nil {
		return 0, cp.UploadResponse{}, bodyErr(err)
	}
	if len(raw) > cp.MaxMetadata {
		return 0, cp.UploadResponse{}, refuse(http.StatusBadRequest, "metadata_too_large")
	}
	meta, err := parseMetadata(raw)
	if err != nil {
		return 0, cp.UploadResponse{}, err
	}

	part, err = mr.NextPart()
	if err != nil || part.FormName() != "audio" {
		return 0, cp.UploadResponse{}, refuse(http.StatusBadRequest, "audio_missing")
	}
	if mt, _, err := mime.ParseMediaType(part.Header.Get("Content-Type")); err != nil || mt != "audio/ogg" {
		return 0, cp.UploadResponse{}, refuse(http.StatusUnsupportedMediaType, "unsupported_media_type")
	}
	tmp, sum, size, err := rs.receive(part)
	if err != nil {
		return 0, cp.UploadResponse{}, err
	}
	defer os.Remove(tmp) // a no-op once renamed into place
	if size != meta.Size {
		return 0, cp.UploadResponse{}, refuse(http.StatusBadRequest, "size_mismatch")
	}
	if hex.EncodeToString(sum) != meta.SHA256 {
		return 0, cp.UploadResponse{}, refuse(http.StatusBadRequest, "sha256_mismatch")
	}
	return rs.store(ctx, rec, meta, tmp, sum, size)
}

func bodyErr(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return refuse(http.StatusRequestEntityTooLarge, "too_large")
	}
	return refuse(http.StatusBadRequest, "malformed_multipart")
}

// receive writes the audio part to a temporary file on the recordings
// volume, hashing it on the way.
func (rs *Recorders) receive(part io.Reader) (path string, sum []byte, size int64, err error) {
	f, err := os.CreateTemp(filepath.Join(rs.cfg.RecordingsDir, incomingDir), "upload-*")
	if err != nil {
		return "", nil, 0, err
	}
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
		if err != nil {
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	head := &prefix{n: 4}
	size, err = io.Copy(io.MultiWriter(f, h, head), io.LimitReader(part, cp.MaxRecording+1))
	if err != nil {
		return "", nil, 0, bodyErr(err)
	}
	if size > cp.MaxRecording {
		return "", nil, 0, refuse(http.StatusRequestEntityTooLarge, "too_large")
	}
	if !bytes.Equal(head.b, []byte("OggS")) {
		return "", nil, 0, refuse(http.StatusUnsupportedMediaType, "not_ogg")
	}
	if err := f.Sync(); err != nil {
		return "", nil, 0, err
	}
	return f.Name(), h.Sum(nil), size, nil
}

// prefix keeps the first n bytes written to it.
type prefix struct {
	n int
	b []byte
}

func (p *prefix) Write(b []byte) (int, error) {
	if k := p.n - len(p.b); k > 0 {
		p.b = append(p.b, b[:min(k, len(b))]...)
	}
	return len(b), nil
}

var requiredMetadata = []string{"sys_num", "call_num", "start_time", "short_name", "talkgroup",
	"start_time_ms", "stop_time_ms", "sha256", "size"}

func parseMetadata(raw []byte) (cp.RecordingMetadata, error) {
	var m cp.RecordingMetadata
	var keys map[string]json.RawMessage
	if !utf8.Valid(raw) || json.Unmarshal(raw, &keys) != nil {
		return m, refuse(http.StatusBadRequest, "malformed_metadata")
	}
	for _, k := range requiredMetadata {
		if _, ok := keys[k]; !ok {
			return m, refuse(http.StatusBadRequest, "missing_"+k)
		}
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			return m, refuse(http.StatusBadRequest, "invalid_"+te.Field)
		}
		return m, refuse(http.StatusBadRequest, "malformed_metadata")
	}
	switch {
	case len(m.ShortName) < 1 || len(m.ShortName) > 64:
		return m, refuse(http.StatusBadRequest, "invalid_short_name")
	case m.StopTimeMS < m.StartTimeMS:
		return m, refuse(http.StatusBadRequest, "invalid_stop_time_ms")
	case m.Size < 1 || m.Size > cp.MaxRecording:
		return m, refuse(http.StatusBadRequest, "invalid_size")
	}
	if b, err := hex.DecodeString(m.SHA256); err != nil || len(b) != sha256.Size || hex.EncodeToString(b) != m.SHA256 {
		return m, refuse(http.StatusBadRequest, "invalid_sha256")
	}
	for _, t := range m.Transmissions {
		if t.OffsetMS < 0 || t.OffsetMS > math.MaxInt32 || t.DurationMS < 0 || t.DurationMS > math.MaxInt32 {
			return m, refuse(http.StatusBadRequest, "invalid_transmissions")
		}
	}
	return m, nil
}

// store files the recording against its call, creating the call if its live
// half never reached the server, unless store.Admit refuses it (R12, R17,
// R18), when the client is told so and the file is dropped.
func (rs *Recorders) store(ctx context.Context, rec store.Recorder, m cp.RecordingMetadata, tmp string, sum []byte, size int64) (int, cp.UploadResponse, error) {
	var (
		code    int
		resp    cp.UploadResponse
		renamed string
		adm     store.Admission
	)
	err := rs.st.InTx(ctx, func(_ pgx.Tx, q *db.Queries) error {
		var err error
		adm, err = store.Admit(ctx, q, store.HeardCall{
			CallKey:   store.CallKey{RecorderID: rec.ID, SysNum: m.SysNum, CallNum: m.CallNum, StartTime: m.StartTime},
			ShortName: m.ShortName, Talkgroup: m.Talkgroup, Patched: m.PatchedTalkgroups,
		})
		if err != nil {
			return err
		}
		if adm.Rejected != "" {
			code, resp = http.StatusOK, cp.UploadResponse{Status: cp.StatusRejected, Reason: string(adm.Rejected)}
			return nil // committed: the rejection is counted
		}
		row, err := q.LockCallForRecording(ctx, db.LockCallForRecordingParams{
			RecorderID: rec.ID, SysNum: int32(m.SysNum), CallNum: m.CallNum, StartTime: m.StartTime,
			SystemID: adm.SystemID, ShortName: m.ShortName, Talkgroup: m.Talkgroup,
			PatchedTalkgroups: nonNil(m.PatchedTalkgroups),
			StartedAt:         tsMS(m.StartTimeMS), EndedAt: tsMS(m.StopTimeMS),
		})
		if err != nil {
			return fmt.Errorf("lock call: %s", store.SafeError(err))
		}
		id := uuidString(row.ID)
		if row.RecordingSha256 != nil {
			stored := hex.EncodeToString(row.RecordingSha256)
			if bytes.Equal(row.RecordingSha256, sum) {
				code, resp = http.StatusOK, cp.UploadResponse{Status: cp.StatusDuplicate, CallID: id, SHA256: stored}
				return nil
			}
			return &uploadError{code: http.StatusConflict, resp: cp.UploadResponse{Error: "identity_conflict", SHA256: stored}}
		}

		rel := recordings.Path(id)
		final := filepath.Join(rs.cfg.RecordingsDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
			return err
		}
		if err := q.SetCallRecording(ctx, db.SetCallRecordingParams{
			ID: row.ID, ShortName: m.ShortName, Talkgroup: m.Talkgroup,
			PatchedTalkgroups: nonNil(m.PatchedTalkgroups),
			StartedAt:         tsMS(m.StartTimeMS), EndedAt: tsMS(m.StopTimeMS),
			RecordingPath: &rel, RecordingSize: &size, RecordingSha256: sum,
		}); err != nil {
			return fmt.Errorf("set recording: %s", store.SafeError(err))
		}
		// The recording is the source of truth for transmissions.
		if err := q.DeleteTransmissions(ctx, row.ID); err != nil {
			return fmt.Errorf("transmissions: %s", store.SafeError(err))
		}
		for _, t := range m.Transmissions {
			d := int32(t.DurationMS)
			if err := q.AddTransmission(ctx, db.AddTransmissionParams{CallID: row.ID, OffsetMs: int32(t.OffsetMS),
				Unit: int64(t.Unit), DurationMs: &d}); err != nil {
				return fmt.Errorf("transmissions: %s", store.SafeError(err))
			}
		}
		// Last, so a failure above leaves no file behind. If the commit then
		// fails, the file is removed below.
		if err := os.Rename(tmp, final); err != nil {
			return err
		}
		renamed = final
		code, resp = http.StatusCreated, cp.UploadResponse{Status: cp.StatusStored, CallID: id,
			SHA256: hex.EncodeToString(sum), Size: size}
		return nil
	})
	if err != nil {
		if renamed != "" {
			os.Remove(renamed)
		}
		return 0, cp.UploadResponse{}, err
	}
	rs.Stat.admitted(adm)
	if code == http.StatusCreated {
		rs.touchCall(ctx, rec, m.StartTimeMS)
	}
	return code, resp, nil
}
