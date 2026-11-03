package main

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func clipboardPacket(status, mime, payload string) string {
	metadata := "5522;type=read:status=" + status
	if mime != "" {
		metadata += ":mime=" + base64.StdEncoding.EncodeToString([]byte(mime))
	}
	return metadata + ";" + base64.StdEncoding.EncodeToString([]byte(payload))
}

func TestClipboard5522PasteRequest(t *testing.T) {
	for _, primary := range []bool{false, true} {
		for _, token := range []string{"", "c2VjcmV0"} {
			var c clipboardPaste
			metadata := "5522;type=read:status=OK"
			if primary {
				metadata += ":loc=primary"
			}
			if token != "" {
				metadata += ":pw=" + token
			}
			c.receive(metadata)
			// Listing packets are independently padded and may split a MIME name.
			c.receive(clipboardPacket("DATA", ".", " \ttext/plain image/jpe"))
			c.receive(clipboardPacket("DATA", ".", "g\nimage/png image/gif\r\n"))
			msg := c.receive("5522;type=read:status=DONE")
			request, ok := msg.(clipboardRequestMsg)
			if !ok {
				t.Fatalf("no request for listed image: %T", msg)
			}
			want := "\x1b]5522;type=read"
			if primary {
				want += ":loc=primary"
			}
			if token != "" {
				want += ":pw=" + token + ":name=UGFzdGUgZXZlbnQ="
			}
			want += ";aW1hZ2UvcG5n\x1b\\"
			if string(request) != want || c.active {
				t.Fatalf("request %q, want %q; active=%v", request, want, c.active)
			}
		}
	}
}

func TestClipboard5522ChunkedImage(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(pastePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, terminator := range []string{"\a", "\x1b\\"} {
		wire := "k\x1b]5522;type=read:status=OK" + terminator
		for i := 0; i < len(data); i += 7 {
			wire += "\x1b]" + clipboardPacket("DATA", "image/png", string(data[i:min(i+7, len(data))])) + terminator
		}
		wire += "\x1b]5522;type=read:status=DONE" + terminator + "p"
		for _, chunkSize := range []int{1, 2, 17, 255, len(wire)} {
			t.Run(fmt.Sprintf("terminator=%q/chunks=%d", terminator, chunkSize), func(t *testing.T) {
				r := &imagePasteReader{}
				defer r.stop()
				var keys strings.Builder
				images := 0
				for i := 0; i < len(wire); i += chunkSize {
					out, packets := r.filter([]byte(wire[i:min(i+chunkSize, len(wire))]))
					keys.Write(out)
					for _, packet := range packets {
						if msg := r.clipboard.receive(packet); msg != nil {
							imageMsg, ok := msg.(pastedImageMsg)
							if !ok || imageMsg.image.Bounds().Dx() != 4 {
								t.Fatalf("unexpected response: %T", msg)
							}
							images++
						}
					}
				}
				if images != 1 || keys.String() != "kp" {
					t.Fatalf("images=%d, keys=%q", images, keys.String())
				}
			})
		}
	}
}

func TestClipboard5522AbortedTransfers(t *testing.T) {
	for _, badPacket := range []string{
		"5522;type=read:status=EPERM",
		"5522;type=read:status=EBUSY",
		"5522;type=read:status=ENOSYS",
		"5522;type=read:status=DATA:mime=aW1hZ2UvcG5n;%%%",
		"5522;type=read:status=DATA:mime=%%% ;YQ==",
		"5522;type=read:status=DATA:mime=aW1hZ2UvcG5n;YQ==\n",
		"5522;type=read:status=DATA:mime=aW1hZ2UvcG5n;YQ",
		"5522;type=read:status=DATA:mime=aW1hZ2UvcG5n:pw=%%% ;YQ==",
		"5522;type=read:status=DATA:status=DONE",
	} {
		var c clipboardPaste
		c.receive("5522;type=read:status=OK")
		c.receive(badPacket)
		if c.active || c.receive("5522;type=read:status=DONE") != nil {
			t.Fatalf("invalid transfer was retained: %q", badPacket)
		}
		// A fresh paste recovers after errors; plain text produces no request.
		c.receive("5522;type=read:status=OK")
		c.receive(clipboardPacket("DATA", ".", "text/plain text/html\n"))
		if c.receive("5522;type=read:status=DONE") != nil {
			t.Fatal("requested unsupported clipboard content")
		}
	}
}
