//go:build !js && !(windows && (amd64 || arm64))

package e2e

// cookieViewApp is TestCookieViewReleased's helper, on Windows only.
func cookieViewApp() {}
