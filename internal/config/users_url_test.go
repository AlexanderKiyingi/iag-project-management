package config

import "testing"

func TestUsersAPIURL(t *testing.T) {
	if got := usersAPIURL("http://localhost:8080", ""); got != "http://localhost:8080/api/v1/users" {
		t.Fatalf("gateway base: got %q", got)
	}
	if got := usersAPIURL("", ""); got != "http://localhost:8080/api/v1/users" {
		t.Fatalf("default: got %q", got)
	}
	if got := usersAPIURL("", "http://users:3005"); got != "http://users:3005" {
		t.Fatalf("explicit direct: got %q", got)
	}
}
