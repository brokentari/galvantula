// Package parse reads one log line into its parts.
package parse

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Level is a log level. A higher level is more severe.
type Level int

// levels holds the level names, least severe first.
var levels = []string{"DEBUG", "INFO", "WARN", "ERROR"}

func (l Level) String() string { return levels[l] }

// LevelNamed returns the level with the given name, such as WARN.
func LevelNamed(name string) (Level, error) {
	i := slices.Index(levels, name)
	if i < 0 {
		return 0, fmt.Errorf("unknown level %q", name)
	}
	return Level(i), nil
}

// Time parses a UTC timestamp such as 2024-03-05T10:15:30Z.
func Time(s string) (time.Time, error) {
	return time.Parse("2006-01-02T15:04:05Z", s)
}

// Entry is one parsed log line.
type Entry struct {
	Raw       string
	Time      time.Time
	Level     Level
	Component string
	Fields    map[string]string
}

// Line parses one log line, such as
// 2024-03-05T10:15:30Z ERROR auth user=ana msg="bad password".
func Line(raw string) (Entry, error) {
	e := Entry{Raw: raw, Fields: map[string]string{}}
	ts, rest, _ := strings.Cut(raw, " ")
	var err error
	if e.Time, err = Time(ts); err != nil {
		return Entry{}, err
	}
	level, rest, _ := strings.Cut(rest, " ")
	if e.Level, err = LevelNamed(level); err != nil {
		return Entry{}, err
	}
	e.Component, rest, _ = strings.Cut(rest, " ")
	if e.Component == "" {
		return Entry{}, errors.New("no component")
	}
	for rest != "" {
		key, value, ok := strings.Cut(rest, "=")
		if !ok || key == "" || strings.ContainsAny(key, ` "`) {
			return Entry{}, fmt.Errorf("bad field %q", rest)
		}
		if quoted, ok := strings.CutPrefix(value, `"`); ok {
			if value, rest, ok = unquote(quoted); !ok {
				return Entry{}, fmt.Errorf("no closing quote in %q", quoted)
			}
			if rest != "" {
				if rest, ok = strings.CutPrefix(rest, " "); !ok {
					return Entry{}, fmt.Errorf("no space after %q", value)
				}
			}
		} else {
			value, rest, _ = strings.Cut(value, " ")
		}
		e.Fields[key] = value
	}
	return e, nil
}

// unquote reads a quoted value up to its closing quote. A backslash escapes
// the next character. It returns the value and the text after the quote.
func unquote(s string) (value, rest string, ok bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
			if i < len(s) {
				b.WriteByte(s[i])
			}
		case '"':
			return b.String(), s[i+1:], true
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", false
}
