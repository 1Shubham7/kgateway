# EP-13455: TLSRoute Multi-Route Filter Chain

* Issue: [#12915](https://github.com/kgateway-dev/kgateway/issues/13455)

## Background

When creating a shared gateway and applying multiple TLSRoutes only the first TLSRoute is accepted whereas the others are ignored. More details [here](https://github.com/kgateway-dev/kgateway/issues/13455)

### How the old code worked (and why it broke)

In `gateway_listener_translator.go`, the function `AppendTlsListener` collected all TLSRoutes for a listener into one  `tcpFilterChain` object.

```go
parent := tcpFilterChainParent{
    routesWithHosts: routeInfos, // all TLSRoutes in one slice
}
fc := tcpFilterChain{parents: parent}
```

Then in `translateTcpFilterChain`, when it came time to actually produce Envoy config, it had to pick one route from that bundle (because Envoy needs a single backend per filter chain). It did this using `slices.MinFunc` - picking the route with the oldest creation timestamp:

```go
r := slices.MinFunc(parent.routesWithHosts, func(a, b *query.RouteInfo) int {
    return a.CreationTimestamp.Compare(b.CreationTimestamp)
})
```

Resulting in the bug mentioned above - Route A (oldest) gets `Accepted: True`. Routes B, C, D... lose the MinFunc election and are silently dropped. The system then reported them `Accepted: False` because they produced no Envoy output.

## The Partial fix I created

Instead of bundling all routes into one filter chain, I created one Envoy filter chain per TLSRoute.

This fixed the original "only first route accepted" bug. All routes now show `Accepted: True`. However, these problems were introduced.

### Problem 1

Our fix creates one filter chain per TLSRoute. Each filter chain has a `serverNames` match like `["foo.example.com"]`. If two different TLSRoutes both claim `foo.example.com`, we'd generate two filter chains with `serverNames: ["foo.example.com"]`.

Envoy evaluates filter chains in the order they appear in the config. It picks the first matching one and stops. The second chain with the same SNI can never be reached - no connection will ever match it. But our code reports both routes as `Accepted: True`. This is misleading and incorrect.

e.g.

```yaml
# Route A (created Jan 1)
TLSRoute A: hostnames: ["foo.example.com"]

# Route B (created Jan 2)
TLSRoute B: hostnames: ["foo.example.com"]
```

**What our fix generates:**
```yaml
filterChains:
- filterChainMatch:
    serverNames: ["foo.example.com"]
  filters: [tcp_proxy → backend-A]
- filterChainMatch:
    serverNames: ["foo.example.com"]  # Route B's chain - UNREACHABLE
  filters: [tcp_proxy → backend-B]
```

Both routes show `Accepted: True`, but Route B's backend never receives any traffic.

### Problem 2

TCPRoutes have no hostname field. They do not use SNI at all - they match all TCP connections arriving on the port.

If we apply the same "one chain per route" logic to TCPRoutes, we get multiple catch-all chains. Just like the duplicate SNI problem, Envoy only ever matches the first one. All subsequent TCPRoutes on the same listener are unreachable but appear `Accepted: True`.

e.g. 

```yaml
TCPRoute A → backend-A
TCPRoute B → backend-B
```

**What our fix generates:**
```yaml
filterChains:
- {}  
  filters: [tcp_proxy → backend-A]
- {}  # NEVER REACHED
  filters: [tcp_proxy → backend-B]
```

### Problem 3

A single TLSRoute can serve multiple hostnames:

```yaml
TLSRoute A:
  hostnames:
  - foo.example.com
  - bar.example.com    # same backend, two different SNIs
  rules:
  - backendRefs: [backend-A]
```

Our fix used this helper:
```go
func tlsRouteHostname(route *query.RouteInfo, listenerHostname *gwv1.Hostname) *gwv1.Hostname {
    hosts := route.Hostnames()
    if len(hosts) > 0 {
        h := gwv1.Hostname(hosts[0])  // ← only takes the FIRST hostname!
        return &h
    }
    return listenerHostname
}
```

So the filter chain for Route A only matches `foo.example.com`. Traffic arriving with SNI `bar.example.com` never matches and fails.

Envoy's `filterChainMatch.serverNames` is a list - it supports multiple SNI values in a single filter chain. We should use all of the route's effective hostnames, not just the first.

Additionally, there is another bug here: `RouteInfo.Hostnames()` only handles `*ir.HttpRouteIR` and returns an empty list for TLSRoutes. The actual intersected hostnames are stored in `RouteInfo.HostnameOverrides`. So even if we'd tried to read all hostnames, we'd have gotten nothing back.

### Problem 4

Envoy does NOT automatically pick "most specific" for TCP filter chains. This is a critical distinction.

For HTTP routes, Envoy is smart - it knows about virtual hosts and will pick the most specific hostname match even if a wildcard comes first in the config. But for TCP filter chains (which is what TLS passthrough uses), Envoy works differently: it goes through the filter chains in the order they appear in the config and picks the first one that matches. Full stop.

So if the config looks like this:

```
filterChains:
- filterChainMatch:
    serverNames: ["*.example.com"]   # checked FIRST
- filterChainMatch:
    serverNames: ["foo.example.com"] # checked SECOND, never reached
```

envoy will say:

```yaml
filterChains:
- filterChainMatch:
    serverNames: ["*.example.com"]
- filterChainMatch:
    serverNames: ["foo.example.com"] # never reached
```

When a connection arrives with SNI foo.example.com:

- Envoy asks: "Does foo.example.com match *.example.com?" -> YES -> use this chain, stop.
- The exact foo.example.com chain is never evaluated.

So our code should have proper ordering of filter chains. We must put exact hostnames before wildcards before catch-alls.

Issue in our code:

```go
func tlsRouteHostname(route *query.RouteInfo, listenerHostname *gwv1.Hostname) *gwv1.Hostname {
    hosts := route.Hostnames()
    if len(hosts) > 0 {
        return &hosts[0]      // ← uses route's hostname
    }
    return listenerHostname   // ← falls back to listener's hostname
}
```

We were using this to set the sniDomain label on the filter chain. The logic was: "if the route has a hostname, prefer that, otherwise use the listener's hostname."

but that is not what "most specific" means in the Gateway API spec. The spec says: "Most specific" = foo.example.com is more specific than *.example.com It's purely about the hostname string format, not about where the hostname is defined (route vs listener)

What actually happens is:

The route's hostnames and the listener's hostname are intersected first (this is done upstream in `query/route.go`). The result of that intersection is what the route is actually allowed to serve.

Among all routes' intersected hostnames, specificity is determined purely by the hostname string pattern.

Example:

Listener hostname = *.example.com,
Route A hostnames = ["foo.example.com"],
Route B hostnames = ["*.example.com"]

After intersection:

Route A's effective hostname = foo.example.com (exact - more specific)
Route B's effective hostname = *.example.com (wildcard - less specific)

So Route A's chain must go first.

## Solution I would like to propose

### Group by hostname, not by route

The fundamental insight is: Envoy filter chains are keyed by SNI hostname. The correct approach is to think about which route "owns" each hostname, then build one filter chain per route that includes all the hostnames that route owns.

### Algo

#### Step 1 - Read effective hostnames correctly

Each route's effective hostnames are already computed upstream by `hostnameIntersect()` in `query/route.go` and stored in `RouteInfo.HostnameOverrides`. Fix `RouteInfo.Hostnames()` to return these for TLSRoutes:

```go
func (r *RouteInfo) Hostnames() []string {
    if len(r.HostnameOverrides) > 0 {
        return r.HostnameOverrides  // intersected result from query layer
    }
    switch typed := r.Object.(type) {
    case *ir.HttpRouteIR:
        return typed.Hostnames
    case *ir.TlsRouteIR:
        return typed.GetHostnames()
    }
    return []string{}
}
```

#### Step 2 - Separate TLSRoutes from TCPRoutes

```
tlsRoutes <- all routeInfos where Object is *TlsRouteIR
tcpRoutes <- all routeInfos where Object is *TcpRouteIR
```

#### Step 3 - Conflict resolution for TLSRoutes (hostname -> winner map)

Build a map from each hostname to the route that should "own" it. The tie-breaker is creation timestamp: the oldest route wins any contested hostname.

```
hostnameOwner = {}

for each route in tlsRoutes (sorted oldest-first):
    for each hostname in route.HostnameOverrides:
        if hostname not already in hostnameOwner:
            hostnameOwner[hostname] = route   <- this route wins this hostname
        # else: another older route already owns it, this route loses this hostname
```

After this map is built:
- Routes that own all their hostnames -> `Accepted: True`, produce a filter chain
- Routes that own some hostnames -> `Accepted: True` (partial), produce a filter chain with only the hostnames they won
- Routes that own no hostnames -> `Accepted: False`, reason: hostname conflict, no filter chain

#### Step 4 - Build one filter chain per winning TLSRoute

After Step 3 we have the hostnameOwner map. Now we need to turn this into actual Envoy filter chains. One filter chain per winning route. so "for each unique winning route, collect all the hostnames it won, then build one filter chain with all of them"

```
Look at the hostnameOwner map.
For each route that appears in it (only once, even if it appears multiple times):
    Collect all hostnames where this route is the owner
    Build one filter chain with those hostnames
```

#### Step 5 - handle TCPRoutes (at most one catch-all)

TCPRoutes have no hostname. They match everything. So in Envoy terms they produce a filter chain with no serverNames - a catch-all. problem is if you have two catch-alls, the second one is always unreachable. Envoy picks the first one for every connection, forever.

```
Look at all TCPRoutes attached to this listener.

If there are zero -> nothing to do, no catch-all chain needed.

If there is one -> it wins, build a catch-all filter chain for it.

If there are multiple -> oldest wins, everyone else gets Accepted: False.
```

Or in simpler terms:

```
if len(tcpRoutes) > 1:
    winner = oldest tcpRoute
    all others -> Accepted: False (conflict)
elif len(tcpRoutes) == 1:
    winner = tcpRoutes[0]

if winner exists:
    create one tcpFilterChain with no serverNames (catch-all)
```

#### Step 6 - Order filter chains before appending to the listener

Sort chains so more specific matches come first. precedence is from up to down:

```
1. Exact hostname chains  (e.g. serverNames: ["foo.example.com"])
2. Wildcard hostname chains  (e.g. serverNames: ["*.example.com"])
3. TCPRoute catch-all (no serverNames)
```

Final ordered list handed to Envoy:

```yaml
filterChains:
  - filterChainMatch:
      serverNames: ["foo.example.com"]  # exact
  - filterChainMatch:
      serverNames: ["*.example.com"]    # wildcard
  - filterChainMatch: {}                # catch-all
```
This ordering ensures Envoy picks the most precise match for any incoming connection.

---

## Examples for problematic scenarios

### Case 1: Two TLSRoutes with different hostnames ✅

```yaml
Route A: hostnames: ["foo.example.com"]  → wins foo.example.com
Route B: hostnames: ["bar.example.com"]  → wins bar.example.com
```
Result: Two filter chains, both Accepted: True. No conflict.

```yaml
filterChains:
- filterChainMatch: {serverNames: ["foo.example.com"]}  # Route A
- filterChainMatch: {serverNames: ["bar.example.com"]}  # Route B
```

### Case 2: Two TLSRoutes with the same hostname

```yaml
Route A: hostnames: ["foo.example.com"]  created: Jan 1
Route B: hostnames: ["foo.example.com"]  created: Jan 2
```

Conflict: Route A is older - Route A wins `foo.example.com`. Route B loses all hostnames.

Result:
- Route A → `Accepted: True`, filter chain `serverNames: ["foo.example.com"]`
- Route B → `Accepted: False`, reason: HostnameConflict, no filter chain

### Case 3: TLSRoute with multiple hostnames

```yaml
Route A: hostnames: ["foo.example.com", "bar.example.com"]
```
Result: One filter chain with both hostnames.

```yaml
filterChains:
- filterChainMatch:
    serverNames: ["foo.example.com", "bar.example.com"]
```

### Case 4: Wildcard TLSRoute + exact TLSRoute

```yaml
Route A: hostnames: ["*.example.com"]   → wins *.example.com
Route B: hostnames: ["foo.example.com"] → wins foo.example.com (more specific, no conflict)
```
Result: Two filter chains, exact first.

```yaml
filterChains:
- filterChainMatch: {serverNames: ["foo.example.com"]} 
- filterChainMatch: {serverNames: ["*.example.com"]} 
```

Traffic to `foo.example.com` is caught by Route B's chain. Traffic to `anything-else.example.com` falls through to Route A's chain.

### Case 5: TLSRoute + TCPRoute

```yaml
Route A (TLSRoute): hostnames: ["foo.example.com"]
Route B (TCPRoute): no hostname
```
Result: TLS chain first, TCP catch-all last.

```yaml
filterChains:
- filterChainMatch: {serverNames: ["foo.example.com"]}
- {}  
```

### Case 6: Two TCPRoutes

```yaml
Route A (TCPRoute): created Jan 1
Route B (TCPRoute): created Jan 2
```
Result: Route A wins (oldest). Route B -> `Accepted: False`.

```yaml
filterChains:
- {} 
```

### Case 7: No routes

In this case, one empty filter chain must still be created so the downstream error-reporting logic detects `len(TcpFilterChains) > 0 && len(matchedTcpListeners) == 0` and reports `Programmed: False` on the listener, telling the user that nothing is configured.

---

## Open Questions

1. What Reason value should be used when a TLSRoute loses a hostname conflict? I can see that the spec defines NoMatchingListenerHostname but that's for when no listener matches at all. maybe we should have custom reason like HostnameConflict.

2. Should a route that wins some of its hostnames (partial conflict, check design doc for e.g.) report Accepted: True or a separate partial condition?
