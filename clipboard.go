package main

import (
	"encoding/base64"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const clipboardPasteEnable = "\x1b[?5522h"
const clipboardPasteDisable = "\x1b[?5522l"

// OSC 5522 read replies are transactions: each DATA packet is independently
// base64 encoded, and DONE ends the complete MIME listing or image transfer.
// See https://sw.kovidgoyal.net/kitty/clipboard/.
type clipboardPaste struct {
	active bool
	mime   string
	data   []byte
	loc    string
	pw     string
}

func clipboardBase64(s string) ([]byte, error) {
	// Go's decoder tolerates CR/LF even in Strict mode; OSC 5522 does not.
	if strings.ContainsAny(s, "\r\n") {
		return nil, base64.CorruptInputError(0)
	}
	return base64.StdEncoding.Strict().DecodeString(s)
}

func imageMIME(mime string) bool {
	return mime == "image/png" || mime == "image/jpeg" || mime == "image/gif"
}

func (c *clipboardPaste) receive(osc string) tea.Msg {
	metadata, payload, _ := strings.Cut(strings.TrimPrefix(osc, "5522;"), ";")
	fields := make(map[string]string)
	for _, field := range strings.Split(metadata, ":") {
		key, value, ok := strings.Cut(field, "=")
		_, duplicate := fields[key]
		if !ok || key == "" || duplicate {
			*c = clipboardPaste{}
			return nil
		}
		fields[key] = value
	}
	if fields["type"] != "read" {
		return nil
	}
	if fields["status"] == "OK" {
		*c = clipboardPaste{active: true}
	}
	if !c.active {
		return nil
	}
	if pw := fields["pw"]; pw != "" {
		if _, err := clipboardBase64(pw); err != nil || (c.pw != "" && c.pw != pw) {
			*c = clipboardPaste{}
			return nil
		}
		c.pw = pw
	}
	if loc := fields["loc"]; loc != "" {
		if loc != "primary" || (c.loc != "" && c.loc != loc) {
			*c = clipboardPaste{}
			return nil
		}
		c.loc = loc
	}
	switch fields["status"] {
	case "OK":
		return nil
	case "DATA":
		mime, err := clipboardBase64(fields["mime"])
		if err != nil || len(mime) == 0 {
			*c = clipboardPaste{}
			return nil
		}
		if string(mime) != "." && !imageMIME(string(mime)) {
			return nil
		}
		if c.mime != "" && c.mime != string(mime) {
			return nil
		}
		data, err := clipboardBase64(payload)
		if err != nil || len(data) > maxPasteBytes-len(c.data) {
			*c = clipboardPaste{}
			return nil
		}
		c.mime = string(mime)
		c.data = append(c.data, data...)
		return nil
	case "DONE":
		completed := *c
		*c = clipboardPaste{}
		if completed.mime == "." {
			available := strings.FieldsFunc(string(completed.data), func(r rune) bool {
				return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
			})
			for _, preferred := range []string{"image/png", "image/jpeg", "image/gif"} {
				for _, mime := range available {
					if mime == preferred {
						metadata := "type=read"
						if completed.loc != "" {
							metadata += ":loc=" + completed.loc
						}
						if completed.pw != "" {
							metadata += ":pw=" + completed.pw + ":name=UGFzdGUgZXZlbnQ="
						}
						return clipboardRequestMsg("\x1b]5522;" + metadata + ";" + base64.StdEncoding.EncodeToString([]byte(mime)) + "\x1b\\")
					}
				}
			}
		} else if imageMIME(completed.mime) {
			if img := decodeImageBytes(completed.data); img != nil {
				return pastedImageMsg{img}
			}
		}
	default:
		// Permission denied, unsupported, busy, or malformed replies abort the
		// transfer. Partial data must never be shown or interpreted as keys.
		*c = clipboardPaste{}
	}
	return nil
}
