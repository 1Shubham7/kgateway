//go:build e2e

package basicrouting

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/onsi/gomega"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kgateway-dev/kgateway/v2/pkg/utils/fsutils"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/requestutils/curl"
	"github.com/kgateway-dev/kgateway/v2/test/e2e"
	"github.com/kgateway-dev/kgateway/v2/test/e2e/common"
	testdefaults "github.com/kgateway-dev/kgateway/v2/test/e2e/defaults"
	"github.com/kgateway-dev/kgateway/v2/test/e2e/tests/base"
	testmatchers "github.com/kgateway-dev/kgateway/v2/test/gomega/matchers"
)

var _ e2e.NewSuiteFunc = NewTestingSuite

var (
	// manifests
	serviceManifest          = filepath.Join(fsutils.MustGetThisDir(), "testdata", "service.yaml")
	headlessServiceManifest  = filepath.Join(fsutils.MustGetThisDir(), "testdata", "headless-service.yaml")
	gatewayWithRouteManifest = filepath.Join(fsutils.MustGetThisDir(), "testdata", "gateway-with-route.yaml")

	// objects
	proxyObjectMeta = metav1.ObjectMeta{
		Name:      "gw",
		Namespace: "default",
	}

	// test cases - removed CurlPodManifest
	setup = base.TestCase{
		Manifests: []string{
			gatewayWithRouteManifest,
		},
	}
	testCases = map[string]*base.TestCase{
		"TestGatewayWithRoute": {
			Manifests: []string{serviceManifest},
		},
		"TestHeadlessService": {
			Manifests: []string{headlessServiceManifest},
		},
	}

	listenerHighPort = 8080
	listenerLowPort  = 80
)

// testingSuite is a suite of basic routing / "happy path" tests
type testingSuite struct {
	*base.BaseTestingSuite
}

func NewTestingSuite(ctx context.Context, testInst *e2e.TestInstallation) suite.TestingSuite {
	return &testingSuite{
		base.NewBaseTestingSuite(ctx, testInst, setup, testCases),
	}
}

// SetupSuite sets up the base gateway for native Go HTTP requests
func (s *testingSuite) SetupSuite() {
	s.BaseTestingSuite.SetupSuite()

	// Setup base gateway for native Go HTTP requests
	common.SetupBaseGateway(s.Ctx, s.TestInstallation, types.NamespacedName{
		Namespace: proxyObjectMeta.GetNamespace(),
		Name:      proxyObjectMeta.GetName(),
	})
}

func (s *testingSuite) TestGatewayWithRoute() {
	s.assertSuccessfulResponse()
}

func (s *testingSuite) TestHeadlessService() {
	s.assertSuccessfulResponse()
}

func (s *testingSuite) assertSuccessfulResponse() {
	for _, port := range []int{listenerHighPort, listenerLowPort} {
		common.BaseGateway.Send(
			s.T(),
			&testmatchers.HttpResponse{
				StatusCode: http.StatusOK,
				Body:       gomega.ContainSubstring(testdefaults.NginxResponse),
			},
			curl.WithHostHeader("example.com"),
			curl.WithPort(port),
		)
	}
}
