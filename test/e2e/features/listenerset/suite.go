//go:build e2e

package listenerset

import (
	"context"

	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwxv1a1 "sigs.k8s.io/gateway-api/apisx/v1alpha1"

	"github.com/kgateway-dev/kgateway/v2/pkg/kgateway/translator/listener"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/kubeutils"
	"github.com/kgateway-dev/kgateway/v2/pkg/utils/requestutils/curl"
	"github.com/kgateway-dev/kgateway/v2/test/e2e"
	"github.com/kgateway-dev/kgateway/v2/test/e2e/common"
	"github.com/kgateway-dev/kgateway/v2/test/e2e/defaults"
	"github.com/kgateway-dev/kgateway/v2/test/e2e/tests/base"
)

var _ e2e.NewSuiteFunc = NewTestingSuite

type testingSuite struct {
	*base.BaseTestingSuite
}

func NewTestingSuite(ctx context.Context, testInst *e2e.TestInstallation) suite.TestingSuite {
	return &testingSuite{
		BaseTestingSuite: base.NewBaseTestingSuite(ctx, testInst, setup, testCases,
			base.WithMinGwApiVersion(base.GwApiRequireListenerSets),
		),
	}
}

func (s *testingSuite) SetupSuite() {
	s.BaseTestingSuite.SetupSuite()

	// Setup base gateway for native Go HTTP requests
	common.SetupBaseGateway(s.Ctx, s.TestInstallation, types.NamespacedName{
		Namespace: proxyObjectMeta.GetNamespace(),
		Name:      proxyObjectMeta.GetName(),
	})
}

func (s *testingSuite) TestValidListenerSet() {
	s.expectValidListenerSetAccepted(validListenerSet)

	// Gateway Listener
	// The route attached to the gateway should work on the listener defined on the gateway
	common.BaseGateway.Send(
		s.T(),
		expectOK,
		curl.WithPort(gwListener1Port),
		curl.WithHostHeader("example.com"),
	)

	// The route attached to the listener set should NOT work on the listener defined on the gateway
	common.BaseGateway.Send(
		s.T(),
		expectNotFound,
		curl.WithPort(gwListener1Port),
		curl.WithHostHeader("listenerset.com"),
	)

	// Listener Set Listeners
	// The route attached to the gateway should NOT work on the listener defined on the listener set
	common.BaseGateway.Send(
		s.T(),
		expectNotFound,
		curl.WithPort(ls1Listener1Port),
		curl.WithHostHeader("example.com"),
	)

	// The route attached to the listener set should work on the listener defined on the listener set
	common.BaseGateway.Send(
		s.T(),
		expectOK,
		curl.WithPort(ls1Listener1Port),
		curl.WithHostHeader("listenerset.com"),
	)

	// The route attached to the listener set should not work on the section it did not target
	common.BaseGateway.Send(
		s.T(),
		expectNotFound,
		curl.WithPort(ls1Listener1Port),
		curl.WithHostHeader("listenerset-section.com"),
	)

	// The route attached to the gateway should NOT work on the listener defined on the listener set
	common.BaseGateway.Send(
		s.T(),
		expectNotFound,
		curl.WithPort(ls1Listener2Port),
		curl.WithHostHeader("example.com"),
	)

	// The route attached to the listener set should work on the listener defined on the listener set
	common.BaseGateway.Send(
		s.T(),
		expectOK,
		curl.WithPort(ls1Listener2Port),
		curl.WithHostHeader("listenerset.com"),
	)

	// The route attached to the listener set should work on the section it targets
	common.BaseGateway.Send(
		s.T(),
		expectOK,
		curl.WithPort(ls1Listener2Port),
		curl.WithHostHeader("listenerset-section.com"),
	)
}

func (s *testingSuite) TestInvalidListenerSetNotAllowed() {
	s.expectInvalidListenerSetNotAllowed(invalidListenerSetNotAllowed)

	// The route attached to the gateway should work on the listener defined on the gateway
	common.BaseGateway.Send(
		s.T(),
		expectOK,
		curl.WithPort(gwListener1Port),
		curl.WithHostHeader("example.com"),
	)

	// The listener defined on the invalid listenerset should not work
	// AssertEventualCurlError expects a curl exit code (like 28 = CURLE_OPERATION_TIMEDOUT), not an HTTP status code. Looking at your types.go:
	// Curl exit code 28 means "timeout" - it's testing that the connection fails at the TCP/network level (because the listener doesn't exist), not that you get an HTTP error response.
	// so we are not changing this.
	s.TestInstallation.AssertionsT(s.T()).AssertEventualCurlError(
		s.Ctx,
		defaults.CurlPodExecOpt,
		[]curl.Option{
			curl.WithHost(kubeutils.ServiceFQDN(proxyService.ObjectMeta)),
			curl.WithPort(ls1Listener1Port),
			curl.WithHostHeader("listenerset.com"),
		},
		curlExitErrorCode,
	)
}

func (s *testingSuite) TestInvalidListenerSetNonExistingGW() {
	s.expectInvalidListenerSetUnknown(invalidListenerSetNonExistingGW)

	// The route attached to the gateway should work on the listener defined on the gateway
	common.BaseGateway.Send(
		s.T(),
		expectOK,
		curl.WithPort(gwListener1Port),
		curl.WithHostHeader("example.com"),
	)

	// The listener defined on the invalid listenerset should not work
	s.TestInstallation.AssertionsT(s.T()).AssertEventualCurlError(
		s.Ctx,
		defaults.CurlPodExecOpt,
		[]curl.Option{
			curl.WithPort(ls1Listener1Port),
			curl.WithHostHeader("listenerset.com"),
		},
		curlExitErrorCode,
	)
}

func (s *testingSuite) TestConflictedListenerSet() {
	s.expectGatewayAccepted(proxyService)
	s.expectValidListenerSetAccepted(validListenerSet)
	s.expectConflictedListenerSetConflicted(conflictedListenerSet)

	// The first listener with hostname conflict should work based on listener precedence
	common.BaseGateway.Send(
		s.T(),
		expectOK,
		curl.WithPort(gwListener1Port),
		curl.WithHostHeader("example.com"),
	)

	// The other listener with hostname conflict should not work based on listener precedence
	common.BaseGateway.Send(
		s.T(),
		expectNotFound,
		curl.WithPort(gwListener1Port),
		curl.WithHostHeader("conflicted-listenerset.com"),
	)

	// The first listener with protocol conflict should work based on listener precedence
	common.BaseGateway.Send(
		s.T(),
		expectOK,
		curl.WithPort(ls1Listener1Port),
		curl.WithHostHeader("listenerset.com"),
	)

	// The other listener with protocol conflict should not work based on listener precedence
	common.BaseGateway.Send(
		s.T(),
		expectNotFound,
		curl.WithPort(ls1Listener1Port),
		curl.WithHostHeader("conflicted-listenerset.com"),
	)

	// The listener without any conflict defined on the listenerset should work
	common.BaseGateway.Send(
		s.T(),
		expectOK,
		curl.WithPort(ls3Listener1Port),
		curl.WithHostHeader("conflicted-listenerset.com"),
	)
}

func (s *testingSuite) TestPolicies() {
	// The policy defined on the Gateway should apply to the Gateway listeners
	common.BaseGateway.Send(
		s.T(),
		expectOKWithCustomHeader("policy", "gateway"),
		curl.WithPort(gwListener1Port),
		curl.WithHostHeader("example.com"),
	)

	// The policy defined on the Gateway should apply to the Gateway section it targets
	common.BaseGateway.Send(
		s.T(),
		expectOKWithCustomHeader("policy", "gateway-section"),
		curl.WithPort(gwListener2Port),
		curl.WithHostHeader("example.com"),
	)

	// The policy defined on the Listener Set should apply to the Listener Set listeners
	common.BaseGateway.Send(
		s.T(),
		expectOKWithCustomHeader("policy", "listener-set"),
		curl.WithPort(ls1Listener1Port),
		curl.WithHostHeader("listenerset.com"),
	)

	// The policy defined on the Listener Set should apply to the Listener Set section it targets
	common.BaseGateway.Send(
		s.T(),
		expectOKWithCustomHeader("policy", "listener-set-section"),
		curl.WithPort(ls1Listener2Port),
		curl.WithHostHeader("listenerset.com"),
	)

	// TODO: Update this when we decide if policies should not be inherited
	// The policy defined on the Gateway should apply to the Listener Set listeners
	common.BaseGateway.Send(
		s.T(),
		expectOKWithCustomHeader("policy", "gateway"),
		curl.WithPort(ls2Listener1Port),
		curl.WithHostHeader("listenerset-2.com"),
	)
}

func (s *testingSuite) expectValidListenerSetAccepted(obj client.Object) {
	s.TestInstallation.AssertionsT(s.T()).EventuallyGatewayCondition(s.Ctx, proxyObjectMeta.Name, proxyObjectMeta.Namespace, listener.GatewayConditionAttachedListenerSets, metav1.ConditionTrue)

	s.TestInstallation.AssertionsT(s.T()).EventuallyListenerSetStatus(s.Ctx, obj.GetName(), obj.GetNamespace(),
		gwxv1a1.ListenerSetStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(gwxv1a1.ListenerSetConditionAccepted),
					Status: metav1.ConditionTrue,
					Reason: string(gwxv1a1.ListenerSetReasonAccepted),
				},
				{
					Type:   string(gwxv1a1.ListenerSetConditionProgrammed),
					Status: metav1.ConditionTrue,
					Reason: string(gwxv1a1.ListenerSetReasonProgrammed),
				},
			},
			Listeners: []gwxv1a1.ListenerEntryStatus{
				{
					Name:           "http",
					Port:           gwxv1a1.PortNumber(ls1Listener1Port), //nolint:gosec // G115: test port constant is int, always in valid range
					AttachedRoutes: 1,
					Conditions: []metav1.Condition{
						{
							Type:   string(gwxv1a1.ListenerEntryConditionAccepted),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonAccepted),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionConflicted),
							Status: metav1.ConditionFalse,
							Reason: string(gwv1.ListenerReasonNoConflicts),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionResolvedRefs),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonResolvedRefs),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionProgrammed),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonProgrammed),
						},
					},
				},
				{
					Name:           "http-2",
					Port:           gwxv1a1.PortNumber(ls1Listener2Port), //nolint:gosec // G115: test port constant is int, always in valid range
					AttachedRoutes: 2,
					Conditions: []metav1.Condition{
						{
							Type:   string(gwxv1a1.ListenerEntryConditionAccepted),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonAccepted),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionConflicted),
							Status: metav1.ConditionFalse,
							Reason: string(gwv1.ListenerReasonNoConflicts),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionResolvedRefs),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonResolvedRefs),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionProgrammed),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonProgrammed),
						},
					},
				},
			},
		})
}

func (s *testingSuite) expectInvalidListenerSetNotAllowed(obj client.Object) {
	s.TestInstallation.AssertionsT(s.T()).EventuallyGatewayCondition(s.Ctx, proxyObjectMeta.Name, proxyObjectMeta.Namespace, listener.GatewayConditionAttachedListenerSets, metav1.ConditionFalse)

	s.TestInstallation.AssertionsT(s.T()).EventuallyListenerSetStatus(s.Ctx, obj.GetName(), obj.GetNamespace(),
		gwxv1a1.ListenerSetStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(gwxv1a1.ListenerSetConditionAccepted),
					Status: metav1.ConditionFalse,
					Reason: string(gwxv1a1.ListenerSetReasonNotAllowed),
				},
				{
					Type:   string(gwxv1a1.ListenerSetConditionProgrammed),
					Status: metav1.ConditionFalse,
					Reason: string(gwxv1a1.ListenerSetReasonNotAllowed),
				},
			},
		})
}

func (s *testingSuite) expectInvalidListenerSetUnknown(obj client.Object) {
	s.TestInstallation.AssertionsT(s.T()).EventuallyGatewayCondition(s.Ctx, proxyObjectMeta.Name, proxyObjectMeta.Namespace, listener.GatewayConditionAttachedListenerSets, metav1.ConditionFalse)

	s.TestInstallation.AssertionsT(s.T()).EventuallyListenerSetStatus(s.Ctx, obj.GetName(), obj.GetNamespace(),
		gwxv1a1.ListenerSetStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(gwxv1a1.ListenerSetConditionAccepted),
					Status: metav1.ConditionUnknown,
				},
				{
					Type:   string(gwxv1a1.ListenerSetConditionProgrammed),
					Status: metav1.ConditionUnknown,
				},
			},
		})
}

func (s *testingSuite) expectGatewayAccepted(obj client.Object) {
	s.TestInstallation.AssertionsT(s.T()).EventuallyGatewayStatus(s.Ctx, obj.GetName(), obj.GetNamespace(),
		gwv1.GatewayStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(gwv1.GatewayConditionAccepted),
					Status: metav1.ConditionTrue,
					Reason: string(gwv1.GatewayReasonAccepted),
				},
				{
					Type:   string(gwv1.GatewayConditionProgrammed),
					Status: metav1.ConditionTrue,
					Reason: string(gwv1.GatewayReasonProgrammed),
				},
			},
			Listeners: []gwv1.ListenerStatus{
				{
					Name:           "http",
					AttachedRoutes: 1,
					Conditions: []metav1.Condition{
						{
							Type:   string(gwxv1a1.ListenerEntryConditionAccepted),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonAccepted),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionProgrammed),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonProgrammed),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionConflicted),
							Status: metav1.ConditionFalse,
							Reason: string(gwv1.ListenerReasonNoConflicts),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionResolvedRefs),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonResolvedRefs),
						},
					},
				},
				{
					Name:           "http-2",
					AttachedRoutes: 1,
					Conditions: []metav1.Condition{
						// The first conflicted listener should be accepted based on listener precedence
						{
							Type:   string(gwxv1a1.ListenerEntryConditionAccepted),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonAccepted),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionProgrammed),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonProgrammed),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionConflicted),
							Status: metav1.ConditionFalse,
							Reason: string(gwv1.ListenerReasonNoConflicts),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionResolvedRefs),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonResolvedRefs),
						},
					},
				},
			},
		})
}

func (s *testingSuite) expectConflictedListenerSetConflicted(obj client.Object) {
	s.TestInstallation.AssertionsT(s.T()).EventuallyGatewayCondition(s.Ctx, proxyObjectMeta.Name, proxyObjectMeta.Namespace, listener.GatewayConditionAttachedListenerSets, metav1.ConditionTrue)

	s.TestInstallation.AssertionsT(s.T()).EventuallyListenerSetStatus(s.Ctx, obj.GetName(), obj.GetNamespace(),
		gwxv1a1.ListenerSetStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(gwxv1a1.ListenerSetConditionAccepted),
					Status: metav1.ConditionTrue,
					Reason: string(gwv1.GatewayReasonListenersNotValid),
				},
				{
					Type:   string(gwxv1a1.ListenerSetConditionProgrammed),
					Status: metav1.ConditionTrue,
					Reason: string(gwv1.GatewayReasonListenersNotValid),
				},
			},
			Listeners: []gwxv1a1.ListenerEntryStatus{
				{
					Name:           "gw-listener-hostname-conflict",
					Port:           gwxv1a1.PortNumber(gwListener2Port), //nolint:gosec // G115: test port constant is int, always in valid range
					AttachedRoutes: 1,
					Conditions: []metav1.Condition{
						{
							Type:    string(gwxv1a1.ListenerEntryConditionAccepted),
							Status:  metav1.ConditionFalse,
							Reason:  string(gwv1.ListenerReasonHostnameConflict),
							Message: listener.ListenerMessageHostnameConflict,
						},
						{
							Type:    string(gwxv1a1.ListenerEntryConditionProgrammed),
							Status:  metav1.ConditionFalse,
							Reason:  string(gwv1.ListenerReasonHostnameConflict),
							Message: listener.ListenerMessageHostnameConflict,
						},
						{
							Type:    string(gwxv1a1.ListenerEntryConditionConflicted),
							Status:  metav1.ConditionTrue,
							Reason:  string(gwv1.ListenerReasonHostnameConflict),
							Message: listener.ListenerMessageHostnameConflict,
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionResolvedRefs),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonResolvedRefs),
						},
					},
				},
				{
					Name:           "ls-listener-protocol-conflict",
					Port:           gwxv1a1.PortNumber(ls1Listener2Port), //nolint:gosec // G115: test port constant is int, always in valid range
					AttachedRoutes: 0,
					Conditions: []metav1.Condition{
						{
							Type:    string(gwxv1a1.ListenerEntryConditionAccepted),
							Status:  metav1.ConditionFalse,
							Reason:  string(gwv1.ListenerReasonProtocolConflict),
							Message: listener.ListenerMessageProtocolConflict,
						},
						{
							Type:    string(gwxv1a1.ListenerEntryConditionProgrammed),
							Status:  metav1.ConditionFalse,
							Reason:  string(gwv1.ListenerReasonProtocolConflict),
							Message: listener.ListenerMessageProtocolConflict,
						},
						{
							Type:    string(gwxv1a1.ListenerEntryConditionConflicted),
							Status:  metav1.ConditionTrue,
							Reason:  string(gwv1.ListenerReasonProtocolConflict),
							Message: listener.ListenerMessageProtocolConflict,
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionResolvedRefs),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonResolvedRefs),
						},
					},
				},
				{
					Name:           "http",
					Port:           gwxv1a1.PortNumber(ls3Listener1Port), //nolint:gosec // G115: test port constant is int, always in valid range
					AttachedRoutes: 1,
					Conditions: []metav1.Condition{
						{
							Type:   string(gwxv1a1.ListenerEntryConditionAccepted),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonAccepted),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionConflicted),
							Status: metav1.ConditionFalse,
							Reason: string(gwv1.ListenerReasonNoConflicts),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionResolvedRefs),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonResolvedRefs),
						},
						{
							Type:   string(gwxv1a1.ListenerEntryConditionProgrammed),
							Status: metav1.ConditionTrue,
							Reason: string(gwxv1a1.ListenerEntryReasonProgrammed),
						},
					},
				},
			},
		})
}
