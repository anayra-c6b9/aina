package config

import "testing"

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"retries", "retries", 0},
		{"retires", "retries", 1}, // adjacent swap
		{"endpont", "endpoint", 1},
		{"coldown", "cooldown", 1},
		{"timout", "timeout", 1},
		{"kitten", "sitting", 3},
	}
	for _, tt := range tests {
		if got := editDistance(tt.a, tt.b); got != tt.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSuggest(t *testing.T) {
	tags := []string{"id", "guard", "call", "endpoint", "retries", "timeout", "map", "undo"}
	tests := []struct {
		word, want string
	}{
		{"retires", "retries"},
		{"endpont", "endpoint"},
		{"timout", "timeout"},
		{"mapp", "map"},
		{"banana", ""},
		{"xy", ""}, // short words need distance 1
		{"di", "id"},
	}
	for _, tt := range tests {
		if got := suggest(tt.word, tags); got != tt.want {
			t.Errorf("suggest(%q) = %q, want %q", tt.word, got, tt.want)
		}
	}
}
