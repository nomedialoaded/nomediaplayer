package mpd

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

// fakeMPD answers canned responses and records received commands.
func fakeMPD(t *testing.T, answers map[string]string) (string, *[]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var got []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				conn.Write([]byte("OK MPD 0.24.0\n"))
				rd := bufio.NewReader(conn)
				inList := false
				for {
					line, err := rd.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\n")
					got = append(got, line)
					if line == "command_list_begin" {
						inList = true
						continue
					}
					if inList && line != "command_list_end" {
						continue
					}
					inList = false
					verb, _, _ := strings.Cut(line, " ")
					conn.Write([]byte(answers[verb] + "OK\n"))
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), &got
}

func TestClientParsesAndQuotes(t *testing.T) {
	addr, got := fakeMPD(t, map[string]string{
		"list":   "AlbumArtist: beta\nAlbumArtist: Alpha\nAlbumArtist: \n",
		"find":   "file: x/2.mp3\nTitle: Two\nAlbum: A\nTrack: 2/10\nduration: 61.5\nfile: x/1.mp3\nTitle: One\nAlbum: A\nTrack: 1\n",
		"status": "state: play\nelapsed: 3.5\nduration: 61.5\nvolume: 70\nsong: 0\nnextsong: 1\nplaylistlength: 2\n",
	})
	c := New(addr)
	ctx := context.Background()
	artists, err := c.List(ctx, "albumartist")
	if err != nil || len(artists) != 2 || artists[0] != "Alpha" {
		t.Fatal("list", artists, err)
	}
	songs, err := c.Find(ctx, "albumartist", `AC/DC "live"`)
	if err != nil || len(songs) != 2 || songs[0].Title != "One" || songs[1].Track != 2 || songs[1].Duration != 61.5 {
		t.Fatal("find", songs, err)
	}
	st, err := c.Status(ctx)
	if err != nil || st.State != "play" || st.Volume != 70 || st.NextSong != 1 {
		t.Fatal("status", st, err)
	}
	if err := c.ReplaceAndPlay(ctx, []string{"x/1.mp3", "x/2.mp3"}, 1); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*got, "|")
	for _, want := range []string{`find albumartist "AC/DC \"live\""`, `clear|add "x/1.mp3"|add "x/2.mp3"|play 1|command_list_end`} {
		if !strings.Contains(joined, want) {
			t.Fatal("missing command", want, joined)
		}
	}
}
