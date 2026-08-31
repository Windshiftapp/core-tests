package database

import (
	"net/url"
	"testing"
)

// The split-variable path (DB_TYPE=postgres + POSTGRES_*) hardcoded
// sslmode=disable and could not reach a TLS-requiring server at all (WI-794).
func TestBuildPostgresConnString_SSLModeIsHonoured(t *testing.T) {
	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		got := BuildPostgresConnString(PostgresEnv{
			Host: "db.example.com", Port: "5432",
			User: "windshift", Password: "secret", Database: "windshift",
			SSLMode: mode,
		})
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("sslmode %q: unparseable DSN %q: %v", mode, got, err)
		}
		if sm := u.Query().Get("sslmode"); sm != mode {
			t.Errorf("sslmode %q: got sslmode=%q in %q", mode, sm, got)
		}
	}
}

// Empty SSLMode keeps the historic behaviour so existing deployments that
// never set POSTGRES_SSLMODE are unaffected by the upgrade.
func TestBuildPostgresConnString_EmptySSLModeDefaultsToDisable(t *testing.T) {
	got := BuildPostgresConnString(PostgresEnv{
		Host: "postgres", Port: "5432",
		User: "windshift", Password: "windshift", Database: "windshift",
	})
	want := "postgresql://windshift:windshift@postgres:5432/windshift?sslmode=disable"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if DefaultPostgresSSLMode != "disable" {
		t.Errorf("DefaultPostgresSSLMode = %q, want %q", DefaultPostgresSSLMode, "disable")
	}
}

// The old fmt.Sprintf build corrupted the DSN whenever a password contained a
// URL metacharacter — the '@' terminated userinfo early and the driver dialled
// the wrong host.
func TestBuildPostgresConnString_EscapesCredentials(t *testing.T) {
	const password = "p@ss:w/rd?#1"
	got := BuildPostgresConnString(PostgresEnv{
		Host: "db.example.com", Port: "5432",
		User: "user name", Password: password, Database: "windshift",
		SSLMode: "require",
	})

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("unparseable DSN %q: %v", got, err)
	}
	if u.Host != "db.example.com:5432" {
		t.Errorf("host = %q, want db.example.com:5432 (DSN %q)", u.Host, got)
	}
	if u.User.Username() != "user name" {
		t.Errorf("username = %q, want %q", u.User.Username(), "user name")
	}
	if pw, _ := u.User.Password(); pw != password {
		t.Errorf("password = %q, want %q", pw, password)
	}
	if u.Path != "/windshift" {
		t.Errorf("path = %q, want /windshift", u.Path)
	}
	if sm := u.Query().Get("sslmode"); sm != "require" {
		t.Errorf("sslmode = %q, want require", sm)
	}
}

// IPv6 literals need brackets or the port parses as part of the address.
func TestBuildPostgresConnString_IPv6Host(t *testing.T) {
	got := BuildPostgresConnString(PostgresEnv{
		Host: "2001:db8::1", Port: "5432",
		User: "windshift", Password: "pw", Database: "windshift",
		SSLMode: "require",
	})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("unparseable DSN %q: %v", got, err)
	}
	if u.Hostname() != "2001:db8::1" || u.Port() != "5432" {
		t.Errorf("host/port = %q/%q, want 2001:db8::1/5432 (DSN %q)", u.Hostname(), u.Port(), got)
	}
}

// ensurePostgresTimezoneUTC must leave a caller-supplied sslmode alone — it is
// the only thing that post-processes a DSN before the driver sees it, so this
// is what keeps sslmode working on the POSTGRES_CONNECTION_STRING path too.
func TestEnsurePostgresTimezoneUTC_PreservesSSLMode(t *testing.T) {
	in := "postgresql://u:p@db.example.com:5432/windshift?sslmode=verify-full"
	got := ensurePostgresTimezoneUTC(in)

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("unparseable DSN %q: %v", got, err)
	}
	if sm := u.Query().Get("sslmode"); sm != "verify-full" {
		t.Errorf("sslmode = %q, want verify-full (DSN %q)", sm, got)
	}
	if tz := u.Query().Get("timezone"); tz != "UTC" {
		t.Errorf("timezone = %q, want UTC (DSN %q)", tz, got)
	}
}
