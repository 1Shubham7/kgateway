//go:build e2e

package common

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/test/util/assert"
	"istio.io/istio/pkg/test/util/retry"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kgateway-dev/kgateway/v2/pkg/utils/requestutils/curl"
	"github.com/kgateway-dev/kgateway/v2/test/e2e"
	"github.com/kgateway-dev/kgateway/v2/test/gomega/matchers"
)

func SetupBaseConfig(ctx context.Context, t *testing.T, installation *e2e.TestInstallation, manifests ...string) {
	for _, s := range log.Scopes() {
		s.SetOutputLevel(log.DebugLevel)
	}
	err := installation.ClusterContext.IstioClient.ApplyYAMLFiles("", manifests...)
	assert.NoError(t, err)
}

func SetupBaseGateway(ctx context.Context, installation *e2e.TestInstallation, name types.NamespacedName) {
	address := installation.Assertions.EventuallyGatewayAddress(
		ctx,
		name.Name,
		name.Namespace,
	)
	BaseGateway = Gateway{
		NamespacedName: name,
		Address:        address,
	}
}

type Gateway struct {
	types.NamespacedName
	Address string
}

var BaseGateway Gateway

// common curl exit codes to the corresponding error substrings produced by client.Do
var CurlExitCodes = map[int]string{
	56: "connection reset by peer",
}

func (g *Gateway) Send(t *testing.T, match *matchers.HttpResponse, opts ...curl.Option) {
	fullOpts := append([]curl.Option{curl.WithHost(g.Address)}, opts...)
	retry.UntilSuccessOrFail(t, func() error {
		r, err := curl.ExecuteRequest(fullOpts...)
		if err != nil {
			return err
		}
		defer r.Body.Close()
		mm := matchers.HaveHttpResponse(match)
		success, err := mm.Match(r)
		if err != nil {
			return err
		}
		if !success {
			return fmt.Errorf("match failed: %v", mm.FailureMessage(r))
		}
		return nil
	})
}

func (g *Gateway) SendExpectError(t *testing.T, expectedErr string, opts ...curl.Option) {
	fullOpts := append([]curl.Option{curl.WithHost(g.Address)}, opts...)

	retry.UntilSuccessOrFail(t, func() error {
		r, err := curl.ExecuteRequest(fullOpts...)

		if err == nil {
			// We successfully got an HTTP response — that is the opposite of what we expect
			// so the retry loop tries again (or eventually fails the test).
			if r != nil {
				r.Body.Close()
			}
			return fmt.Errorf("expected a connection-level error but received a successful HTTP response")
		}

		// Unwrap to the root cause and compare
		if expectedErr != "" {
			root := unwrapRoot(err)
			if root.Error() != expectedErr {
				return fmt.Errorf("connection error root cause %q does not match expected %q (full error: %w)",
					root.Error(), expectedErr, err)
			}
		}
		return nil
	})
}

// unwrapRoot walks the errors.Unwrap chain and returns the innermost error.
// For net/http transport failures the chain is:
//
//	*url.Error → *net.OpError → *os.SyscallError → syscall.Errno
func unwrapRoot(err error) error {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			// No further wrapping — this is the root cause.
			return err
		}
		err = inner
	}
}
