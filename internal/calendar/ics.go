package calendar

import (
	"bytes"
	"strconv"
	"strings"
	"time"
)

type Item struct {
	ID    int64
	Title string
	Desc  string
	Start time.Time
	End   time.Time
}

func BuildICS(items []Item) []byte {
	var b bytes.Buffer
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//UpSkill//EN\r\n")
	for _, t := range items {
		b.WriteString("BEGIN:VEVENT\r\n")
		b.WriteString("UID:upskill-" + strconv.FormatInt(t.ID, 10) + "@upskill\r\n")
		b.WriteString("DTSTART:" + t.Start.UTC().Format("20060102T150405Z") + "\r\n")
		b.WriteString("DTEND:" + t.End.UTC().Format("20060102T150405Z") + "\r\n")
		b.WriteString("SUMMARY:" + escapeICS(t.Title) + "\r\n")
		if d := t.Desc; d != "" {
			b.WriteString("DESCRIPTION:" + escapeICS(d) + "\r\n")
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.Bytes()
}

func escapeICS(s string) string {
	repl := strings.NewReplacer("\\", "\\\\", ";", "\\;", ",", "\\,", "\n", "\\n", "\r", "")
	return repl.Replace(s)
}
