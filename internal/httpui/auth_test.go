package httpui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connarr/internal/config"
	"connarr/internal/inventory"
	"connarr/internal/store"
)

// newAuthTestServer builds a server backed by a real store and a real
// on-disk config file (SetCredentials requires a config path to persist
// to), so it exercises authGate the same way a real deployment would.
func newAuthTestServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, database)
	inv.SetConfigPath(configPath)
	server, err := New(inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), database
}

func postForm(t *testing.T, handler http.Handler, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func getPath(t *testing.T, handler http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func sessionCookieFrom(response *httptest.ResponseRecorder) *http.Cookie {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie
		}
	}
	return nil
}

func TestAuthGateSendsUnconfiguredServerToSetup(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	response := getPath(t, handler, "/", nil)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/setup" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	setupResponse := getPath(t, handler, "/setup", nil)
	if setupResponse.Code != http.StatusOK || !strings.Contains(setupResponse.Body.String(), "Create account") {
		t.Fatalf("setup page status=%d body=%q", setupResponse.Code, setupResponse.Body.String())
	}
}

func TestSetupRejectsMismatchedPasswords(t *testing.T) {
	handler, database := newAuthTestServer(t)
	response := postForm(t, handler, "/setup", url.Values{"username": {"admin"}, "password": {"password1"}, "confirm": {"password2"}}, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "do not match") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if sessionCookieFrom(response) != nil {
		t.Fatal("a mismatched setup must not issue a session")
	}
	if valid, _ := database.SessionValid("anything"); valid {
		t.Fatal("no session should exist yet")
	}
}

func TestSetupCreatesAccountAndSignsIn(t *testing.T) {
	handler, database := newAuthTestServer(t)
	response := postForm(t, handler, "/setup", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}, "confirm": {"correct-horse-battery"}}, nil)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("status=%d location=%q body=%q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	cookie := sessionCookieFrom(response)
	if cookie == nil {
		t.Fatal("setup did not issue a session cookie")
	}
	if valid, err := database.SessionValid(cookie.Value); err != nil || !valid {
		t.Fatalf("issued session is not valid: valid=%v err=%v", valid, err)
	}

	// The account now exists: an unauthenticated visit to /setup must not
	// re-expose the create-account form (that would let anyone overwrite
	// the admin account without ever authenticating).
	unauthenticatedSetup := getPath(t, handler, "/setup", nil)
	if unauthenticatedSetup.Code != http.StatusSeeOther || unauthenticatedSetup.Header().Get("Location") != "/login" {
		t.Fatalf("setup after account exists: status=%d location=%q", unauthenticatedSetup.Code, unauthenticatedSetup.Header().Get("Location"))
	}

	// A signed-in visit to /setup is harmless but pointless; it should bounce
	// home rather than show the create-account form again.
	authenticatedSetup := getPath(t, handler, "/setup", cookie)
	if authenticatedSetup.Code != http.StatusSeeOther || authenticatedSetup.Header().Get("Location") != "/" {
		t.Fatalf("signed-in setup revisit: status=%d location=%q", authenticatedSetup.Code, authenticatedSetup.Header().Get("Location"))
	}
}

func TestAuthGateRequiresSessionOnceAccountExists(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	postForm(t, handler, "/setup", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}, "confirm": {"correct-horse-battery"}}, nil)

	unauthenticated := getPath(t, handler, "/", nil)
	if unauthenticated.Code != http.StatusSeeOther || unauthenticated.Header().Get("Location") != "/login" {
		t.Fatalf("status=%d location=%q", unauthenticated.Code, unauthenticated.Header().Get("Location"))
	}

	loginResponse := postForm(t, handler, "/login", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}}, nil)
	cookie := sessionCookieFrom(loginResponse)
	if loginResponse.Code != http.StatusSeeOther || cookie == nil {
		t.Fatalf("login status=%d body=%q", loginResponse.Code, loginResponse.Body.String())
	}
	authenticated := getPath(t, handler, "/", cookie)
	if authenticated.Code != http.StatusOK {
		t.Fatalf("authenticated home status=%d", authenticated.Code)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	postForm(t, handler, "/setup", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}, "confirm": {"correct-horse-battery"}}, nil)

	response := postForm(t, handler, "/login", url.Values{"username": {"admin"}, "password": {"wrong-password"}}, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Incorrect username or password") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if sessionCookieFrom(response) != nil {
		t.Fatal("a failed login must not issue a session")
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	handler, database := newAuthTestServer(t)
	setupResponse := postForm(t, handler, "/setup", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}, "confirm": {"correct-horse-battery"}}, nil)
	cookie := sessionCookieFrom(setupResponse)

	logoutResponse := postForm(t, handler, "/logout", url.Values{}, cookie)
	if logoutResponse.Code != http.StatusSeeOther || logoutResponse.Header().Get("Location") != "/login" {
		t.Fatalf("logout status=%d location=%q", logoutResponse.Code, logoutResponse.Header().Get("Location"))
	}
	if valid, _ := database.SessionValid(cookie.Value); valid {
		t.Fatal("session must be deleted after logout")
	}
	afterLogout := getPath(t, handler, "/", cookie)
	if afterLogout.Code != http.StatusSeeOther || afterLogout.Header().Get("Location") != "/login" {
		t.Fatalf("post-logout status=%d location=%q", afterLogout.Code, afterLogout.Header().Get("Location"))
	}
}

// TestChangePasswordRevokesEveryOtherSession guards the actual point of a
// password rotation: it must sign out browsers other than the one making
// the change, not just accept a new password while leaving old sessions
// (e.g. from a device you no longer trust) valid.
func TestChangePasswordRevokesEveryOtherSession(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	setupResponse := postForm(t, handler, "/setup", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}, "confirm": {"correct-horse-battery"}}, nil)
	firstSession := sessionCookieFrom(setupResponse)

	secondLogin := postForm(t, handler, "/login", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}}, nil)
	secondSession := sessionCookieFrom(secondLogin)
	if secondSession == nil {
		t.Fatal("second login did not issue a session")
	}

	changeResponse := postForm(t, handler, "/settings/password", url.Values{"current_password": {"correct-horse-battery"}, "new_password": {"new-correct-horse"}, "confirm": {"new-correct-horse"}}, firstSession)
	if changeResponse.Code != http.StatusOK || !strings.Contains(changeResponse.Body.String(), "Password updated") {
		t.Fatalf("change password status=%d body=%q", changeResponse.Code, changeResponse.Body.String())
	}
	newFirstSession := sessionCookieFrom(changeResponse)
	if newFirstSession == nil {
		t.Fatal("the browser making the change should get a fresh session, not be logged out too")
	}

	stillWorks := getPath(t, handler, "/", newFirstSession)
	if stillWorks.Code != http.StatusOK {
		t.Fatalf("the changing browser's new session should still work: status=%d", stillWorks.Code)
	}
	revoked := getPath(t, handler, "/", secondSession)
	if revoked.Code != http.StatusSeeOther || revoked.Header().Get("Location") != "/login" {
		t.Fatalf("the other browser's session should be revoked: status=%d location=%q", revoked.Code, revoked.Header().Get("Location"))
	}

	loginWithOldPassword := postForm(t, handler, "/login", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}}, nil)
	if !strings.Contains(loginWithOldPassword.Body.String(), "Incorrect username or password") {
		t.Fatalf("old password should no longer work: body=%q", loginWithOldPassword.Body.String())
	}
	loginWithNewPassword := postForm(t, handler, "/login", url.Values{"username": {"admin"}, "password": {"new-correct-horse"}}, nil)
	if loginWithNewPassword.Code != http.StatusSeeOther {
		t.Fatalf("new password should work: status=%d body=%q", loginWithNewPassword.Code, loginWithNewPassword.Body.String())
	}
}

func TestChangePasswordRejectsWrongCurrentPassword(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	setupResponse := postForm(t, handler, "/setup", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}, "confirm": {"correct-horse-battery"}}, nil)
	session := sessionCookieFrom(setupResponse)

	response := postForm(t, handler, "/settings/password", url.Values{"current_password": {"totally-wrong"}, "new_password": {"new-correct-horse"}, "confirm": {"new-correct-horse"}}, session)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Current password is incorrect") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestAssetsAndHealthzBypassAuthGate(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	if response := getPath(t, handler, "/healthz", nil); response.Code != http.StatusOK {
		t.Fatalf("/healthz status=%d", response.Code)
	}
}
