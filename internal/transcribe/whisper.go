// Package transcribe wraps a local whisper.cpp binary to turn uploaded
// audio (webm/opus, mp4/aac, etc.) into text.
//
// The Transcriber interface returns the final text as a single string
// and calls onSegment for each segment whisper emits while processing —
// clients can render the text as it streams in instead of waiting for
// the whole run to finish.
package transcribe

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MaxUploadBytes caps the audio payload the server will accept. About
// 30s of opus-encoded speech at 32kbps fits in ~120KB; 5MB gives a big
// safety margin for other codecs and stray noise.
const MaxUploadBytes int64 = 5 * 1024 * 1024

// ErrNotConfigured is returned when whisper env vars are unset. Handlers
// surface this as a user-visible "voice is not configured" error.
var ErrNotConfigured = errors.New("whisper binary or model not configured")

// Transcriber turns an audio stream into text, streaming segments out
// via onSegment as they become available.
type Transcriber interface {
	Transcribe(ctx context.Context, audio io.Reader, mime string, onSegment func(text string)) (string, error)
}

// WhisperCLI is a Transcriber backed by the whisper.cpp `whisper-cli`
// binary. It shells out to ffmpeg first to normalize inputs to 16kHz
// mono wav — whisper.cpp requires that format.
type WhisperCLI struct {
	WhisperBin   string // absolute path to whisper-cli
	WhisperModel string // absolute path to the ggml model file
	FFmpegBin    string // "ffmpeg" or an absolute path
}

// New returns a WhisperCLI backed by the given env-configured binaries.
// Returns ErrNotConfigured if either whisperBin or whisperModel is
// empty; callers surface that to the client rather than crashing.
func New(whisperBin, whisperModel, ffmpegBin string) (*WhisperCLI, error) {
	if whisperBin == "" || whisperModel == "" {
		return nil, ErrNotConfigured
	}
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	return &WhisperCLI{
		WhisperBin:   whisperBin,
		WhisperModel: whisperModel,
		FFmpegBin:    ffmpegBin,
	}, nil
}

// Transcribe writes the uploaded audio to a temp file, runs ffmpeg to
// produce a 16kHz mono wav, then runs whisper-cli and streams each
// emitted segment through onSegment. The returned string is the joined
// transcript with surrounding whitespace trimmed.
//
// mime is informational — ffmpeg autodetects the container, so the
// argument just keeps the tempfile suffix honest for debugging.
func (c *WhisperCLI) Transcribe(ctx context.Context, audio io.Reader, mime string, onSegment func(text string)) (string, error) {
	tmpDir, err := os.MkdirTemp("", "kit-chat-audio-")
	if err != nil {
		return "", fmt.Errorf("creating tempdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	inputPath := filepath.Join(tmpDir, "input"+extForMime(mime))
	wavPath := filepath.Join(tmpDir, "input.wav")

	if err := writeTemp(inputPath, audio); err != nil {
		return "", err
	}

	if err := c.convertToWav(ctx, inputPath, wavPath); err != nil {
		return "", fmt.Errorf("ffmpeg: %w", err)
	}

	return c.runWhisper(ctx, wavPath, onSegment)
}

func writeTemp(path string, r io.Reader) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating temp audio file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("writing temp audio file: %w", err)
	}
	return nil
}

func (c *WhisperCLI) convertToWav(ctx context.Context, in, out string) error {
	// -y: overwrite; -ar 16000: whisper requires 16kHz; -ac 1: mono.
	// -loglevel error: suppress normal noise on stderr.
	cmd := exec.CommandContext(ctx, c.FFmpegBin,
		"-y", "-loglevel", "error",
		"-i", in,
		"-ar", "16000", "-ac", "1",
		out,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// runWhisper invokes whisper-cli against the wav at path. We use the
// --no-prints, --no-timestamps, and --output-txt-stdout flags so stdout
// is the transcript and nothing else. whisper.cpp prints segments as it
// produces them when --output-txt-stdout is set, giving us a natural
// streaming hook via bufio line reads.
func (c *WhisperCLI) runWhisper(ctx context.Context, wavPath string, onSegment func(text string)) (string, error) {
	cmd := exec.CommandContext(ctx, c.WhisperBin,
		"-m", c.WhisperModel,
		"-f", wavPath,
		"--no-timestamps",
		"--no-prints",
		"--output-txt",
	)
	// We don't use --output-txt-stdout (not all builds have it). Instead,
	// read stdout directly — whisper-cli prints segments to stdout by
	// default when --no-prints silences the progress noise.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start whisper: %w", err)
	}

	var segments []string
	scanner := bufio.NewScanner(stdout)
	// Whisper segments can be long; bump the buffer.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		segments = append(segments, line)
		if onSegment != nil {
			onSegment(line)
		}
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("whisper exited: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	// Join segments with newlines so multi-sentence dictation keeps
	// its natural line breaks in the transcript textarea. Whisper
	// emits one segment per pause/phrase, which is close to sentence
	// boundaries for most speech.
	return strings.TrimSpace(strings.Join(segments, "\n")), nil
}

// extForMime returns a file extension that keeps ffmpeg happy and makes
// the temp file self-describing. Unknown types fall back to .bin;
// ffmpeg autodetects format by content regardless.
func extForMime(mime string) string {
	switch {
	case strings.HasPrefix(mime, "audio/webm"):
		return ".webm"
	case strings.HasPrefix(mime, "audio/ogg"):
		return ".ogg"
	case strings.HasPrefix(mime, "audio/mp4"):
		return ".m4a"
	case strings.HasPrefix(mime, "audio/mpeg"):
		return ".mp3"
	case strings.HasPrefix(mime, "audio/wav"), strings.HasPrefix(mime, "audio/x-wav"):
		return ".wav"
	default:
		return ".bin"
	}
}
