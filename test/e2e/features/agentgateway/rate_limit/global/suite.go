//go:build e2e

package global

import (
	"context"
	"fmt"
	"net/http"

	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kgateway-dev/kgateway/v2/pkg/utils/requestutils/curl"
	"github.com/kgateway-dev/kgateway/v2/test/e2e"
	"github.com/kgateway-dev/kgateway/v2/test/e2e/common"
	testdefaults "github.com/kgateway-dev/kgateway/v2/test/e2e/defaults"
	testmatchers "github.com/kgateway-dev/kgateway/v2/test/gomega/matchers"
	"github.com/kgateway-dev/kgateway/v2/test/testutils"
)

var _ e2e.NewSuiteFunc = NewTestingSuite

// testingSuite is a suite of global rate limiting tests
type testingSuite struct {
	suite.Suite

	ctx context.Context

	// testInstallation contains all the metadata/utilities necessary to execute a series of tests
	// against an installation of kgateway
	testInstallation *e2e.TestInstallation

	// manifests shared by all tests
	commonManifests []string
	// resources from manifests shared by all tests
	commonResources []client.Object
}

// rlBurstTries: run a tiny burst so all checks stay in one fixed RL window.
// The external rate limiter uses clock-aligned windows (per-minute resets at :00),
// so long loops can straddle the boundary and flake.
// 3 = one to establish state, two to confirm; fewer risks a transient, more risks crossing the window.
var rlBurstTries = 3

func NewTestingSuite(ctx context.Context, testInst *e2e.TestInstallation) suite.TestingSuite {
	return &testingSuite{
		ctx:              ctx,
		testInstallation: testInst,
	}
}

func (s *testingSuite) SetupSuite() {
	s.commonManifests = []string{
		rateLimitServerManifest,
	}
	s.commonResources = []client.Object{
		// rate limit service resources
		rateLimitDeployment, rateLimitService, rateLimitConfigMap,
	}

	// set up common resources once
	for _, manifest := range s.commonManifests {
		err := s.testInstallation.Actions.Kubectl().ApplyFile(s.ctx, manifest)
		s.Require().NoError(err, "can apply "+manifest)
	}
	s.testInstallation.Assertions.EventuallyObjectsExist(s.ctx, s.commonResources...)

	// verify shared resources exist
	s.testInstallation.Assertions.EventuallyObjectsExist(s.ctx, gateway, proxyDeployment, proxyService, proxyServiceAccount)

	// make sure rate limit pods are running
	s.testInstallation.Assertions.EventuallyPodsRunning(s.ctx, rateLimitDeployment.GetNamespace(), metav1.ListOptions{
		LabelSelector: "app=ratelimit",
	})
	// verify proxy pods are running
	s.testInstallation.Assertions.EventuallyPodsRunning(s.ctx, proxyObjectMeta.GetNamespace(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", testdefaults.WellKnownAppLabel, proxyObjectMeta.GetName()),
	})
}

func (s *testingSuite) TearDownSuite() {
	if testutils.ShouldSkipCleanup(s.T()) {
		return
	}
	// clean up only resources this suite created
	for _, manifest := range s.commonManifests {
		err := s.testInstallation.Actions.Kubectl().DeleteFileSafe(s.ctx, manifest)
		s.Require().NoError(err, "can delete "+manifest)
	}
	// wait for rate limit resources to be deleted
	s.testInstallation.Assertions.EventuallyObjectsNotExist(s.ctx, s.commonResources...)

	// make sure rate limit pods are gone
	s.testInstallation.Assertions.EventuallyPodsNotExist(s.ctx, rateLimitDeployment.GetNamespace(), metav1.ListOptions{
		LabelSelector: "app=ratelimit",
	})
}

// Test cases for global rate limit based on remote address (client IP)
func (s *testingSuite) TestGlobalRateLimitByRemoteAddress() {
	s.setupTest([]string{httpRoutesManifest, ipRateLimitManifest}, []client.Object{route, route2, ipRateLimitAgentgatewayPolicy})

	// First request should be successful
	s.assertResponse("/path1", http.StatusOK)

	// Consecutive requests should be rate limited
	s.assertConsistentResponse("/path1", http.StatusTooManyRequests)

	// Second route should also be rate limited since the rate limit is based on client IP
	s.assertConsistentResponse("/path2", http.StatusTooManyRequests)
}

// Test cases for global rate limit based on request path
func (s *testingSuite) TestGlobalRateLimitByPath() {
	s.setupTest([]string{httpRoutesManifest, pathRateLimitManifest}, []client.Object{route, route2, pathRateLimitAgentgatewayPolicy})

	// First request should be successful
	s.assertResponse("/path1", http.StatusOK)

	// Consecutive requests to the same path should be rate limited
	s.assertConsistentResponse("/path1", http.StatusTooManyRequests)

	// Second route shouldn't be rate limited since it has a different path
	s.assertConsistentResponse("/path2", http.StatusOK)
}

// Test cases for global rate limit based on user ID header
func (s *testingSuite) TestGlobalRateLimitByUserID() {
	s.setupTest([]string{httpRoutesManifest, userRateLimitManifest}, []client.Object{route, route2, userRateLimitAgentgatewayPolicy})

	// First request should be successful
	s.assertResponseWithHeader("/path1", "X-User-ID", "user1", http.StatusOK)

	// Consecutive requests from same user should be rate limited
	s.assertConsistentResponseWithHeader("/path1", "X-User-ID", "user1", http.StatusTooManyRequests)

	// Requests from different user shouldn't be rate limited
	s.assertResponseWithHeader("/path1", "X-User-ID", "user2", http.StatusOK)
}

// Test cases for combined local and global rate limiting
func (s *testingSuite) TestCombinedLocalAndGlobalRateLimit() {
	s.setupTest([]string{httpRoutesManifest, combinedRateLimitManifest}, []client.Object{route, route2, combinedRateLimitAgentgatewayPolicy})

	// First request should be successful
	s.assertResponse("/path1", http.StatusOK)

	// Consecutive requests should be rate limited
	s.assertConsistentResponse("/path1", http.StatusTooManyRequests)
}

func (s *testingSuite) setupTest(manifests []string, resources []client.Object) {
	testutils.Cleanup(s.T(), func() {
		for _, manifest := range manifests {
			err := s.testInstallation.Actions.Kubectl().DeleteFileSafe(s.ctx, manifest)
			s.Require().NoError(err)
		}
		s.testInstallation.AssertionsT(s.T()).EventuallyObjectsNotExist(s.ctx, resources...)
	})

	for _, manifest := range manifests {
		err := s.testInstallation.Actions.Kubectl().ApplyFile(s.ctx, manifest)
		s.Require().NoError(err, "can apply "+manifest)
	}
	s.testInstallation.AssertionsT(s.T()).EventuallyObjectsExist(s.ctx, resources...)
}

func (s *testingSuite) assertResponse(path string, expectedStatus int) {
	common.BaseGateway.Send(
		s.T(),
		&testmatchers.HttpResponse{
			StatusCode: expectedStatus,
		},
		curl.WithPath(path),
		curl.WithHostHeader("example.com"),
	)
}

func (s *testingSuite) assertResponseWithHeader(path string, headerName string, headerValue string, expectedStatus int) {
	common.BaseGateway.Send(
		s.T(),
		&testmatchers.HttpResponse{
			StatusCode: expectedStatus,
		},
		curl.WithPath(path),
		curl.WithHostHeader("example.com"),
		curl.WithHeader(headerName, headerValue),
	)
}

// Burst a few quick checks so the test doesn't cross a rate-limit window boundary.
func (s *testingSuite) assertConsistentResponse(path string, expectedStatus int) {
	for range rlBurstTries {
		common.BaseGateway.Send(
			s.T(),
			&testmatchers.HttpResponse{
				StatusCode: expectedStatus,
			},
			curl.WithPath(path),
			curl.WithHostHeader("example.com"),
		)
	}
}

// Safe burst a few quick checks so the test doesn't cross a rate-limit window boundary.
func (s *testingSuite) assertConsistentResponseWithHeader(path, headerName, headerValue string, expectedStatus int) {
	for range rlBurstTries {
		common.BaseGateway.Send(
			s.T(),
			&testmatchers.HttpResponse{
				StatusCode: expectedStatus,
			},
			curl.WithPath(path),
			curl.WithHostHeader("example.com"),
			curl.WithHeader(headerName, headerValue),
		)
	}
}
