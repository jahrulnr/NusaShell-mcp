package main

import (
	"os"
	"strings"
	"time"
)

// mimeToExt maps MIME types to file extensions.
//
// Ported 1:1 from GoClaw (internal/channels/whatsapp/media_download.go).
func mimeToExt(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/jpeg"):
		return ".jpg"
	case strings.HasPrefix(mime, "image/png"):
		return ".png"
	case strings.HasPrefix(mime, "image/webp"):
		return ".webp"
	case strings.HasPrefix(mime, "video/mp4"):
		return ".mp4"
	case strings.HasPrefix(mime, "audio/ogg"):
		return ".ogg"
	case strings.HasPrefix(mime, "audio/mpeg"):
		return ".mp3"
	case strings.HasPrefix(mime, "application/pdf"):
		return ".pdf"
	default:
		return ".bin"
	}
}

// classifyDownloadError returns a human-readable reason for a media download failure.
//
// Ported 1:1 from GoClaw (internal/channels/whatsapp/media_download.go).
func classifyDownloadError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "timeout"
	case strings.Contains(msg, "decrypt") || strings.Contains(msg, "cipher"):
		return "decrypt_error"
	case strings.Contains(msg, "404") || strings.Contains(msg, "not found"):
		return "expired"
	case strings.Contains(msg, "unsupported"):
		return "unsupported"
	default:
		return "unknown"
	}
}

// scheduleMediaCleanup removes temp media files after a delay.
// Uses time.AfterFunc so it does not block.
//
// Ported 1:1 from GoClaw (internal/channels/whatsapp/media_download.go).
func scheduleMediaCleanup(paths []string, delay time.Duration) {
	if len(paths) == 0 {
		return
	}
	time.AfterFunc(delay, func() {
		for _, p := range paths {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				stderr("temp media cleanup failed: %s: %s", p, err)
			}
		}
	})
}
