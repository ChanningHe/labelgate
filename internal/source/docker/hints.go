package docker

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io/fs"
	"strings"

	"github.com/moby/moby/client"

	"github.com/channinghe/labelgate/internal/config"
)

// socketMount is the compose volume that gives labelgate the local Docker socket.
const socketMount = "/var/run/docker.sock:/var/run/docker.sock:ro"

// explain wraps a Docker API failure with how to fix it, when the cause is
// recognisable; otherwise err is returned unchanged.
func explain(endpoint string, err error) error {
	if hint := hintFor(endpoint, err); hint != "" {
		return &hintedError{err: err, hint: hint}
	}
	return err
}

// hintedError appends a fix to an error, keeping it unwrappable.
type hintedError struct {
	err  error
	hint string
}

func (e *hintedError) Error() string { return e.err.Error() + "; " + e.hint }
func (e *hintedError) Unwrap() error { return e.err }

// hintFor returns how to fix err, or "".
func hintFor(endpoint string, err error) string {
	ep := config.KeyWithEnv("docker.endpoint")
	unix := strings.HasPrefix(endpoint, schemeUnix+"://")
	msg := err.Error()
	switch {
	case unix && (errors.Is(err, fs.ErrPermission) || strings.Contains(msg, "permission denied")):
		return "labelgate may not open the Docker socket: add the socket's group ID to the container " +
			"(compose group_add; find it with: stat -c %g " + strings.TrimPrefix(endpoint, "unix://") + ")"
	case unix && (errors.Is(err, fs.ErrNotExist) || client.IsErrConnectionFailed(err)):
		return "no Docker daemon answers at " + endpoint + ": mount the socket into the container (volume " +
			socketMount + ") or set " + ep
	case isUntrustedCert(err):
		return "the Docker daemon's TLS certificate is not trusted: set " + config.KeyWithEnv("docker.tls.ca") +
			" to the CA that signed it"
	case strings.Contains(msg, "certificate required") || strings.Contains(msg, "bad certificate") || strings.Contains(msg, "handshake failure"):
		return "the Docker daemon requires a client certificate: set " + config.KeyWithEnv("docker.tls.cert") +
			" and " + config.KeyWithEnv("docker.tls.key")
	case strings.Contains(msg, "HTTP request to an HTTPS server"):
		return "the Docker daemon expects TLS: set " + config.KeyWithEnv("docker.tls.ca") + " (and a client certificate if it requires one)"
	case client.IsErrConnectionFailed(err):
		return "check " + ep + " and that the Docker daemon listens there"
	}
	return ""
}

// isUntrustedCert reports a server certificate that failed verification.
func isUntrustedCert(err error) bool {
	var verr *tls.CertificateVerificationError
	var uerr x509.UnknownAuthorityError
	var herr x509.HostnameError
	return errors.As(err, &verr) || errors.As(err, &uerr) || errors.As(err, &herr)
}
