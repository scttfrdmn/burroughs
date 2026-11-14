// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// WASI 0.3's name lookup (#1278's E7c, ADR 0126): `wasi:sockets/ip-name-lookup@0.3`'s one function,
//
//	resolve-addresses: async func(name: string) -> result<list<ip-address>, error-code>;
//
// served from the async table, so through both lowers. wasi-libc's getaddrinfo lowers it with no async option,
// which is the blocking sync lower (async_synclower.go); an async lower is served too.
//
// # What resolves inline, and what runs on a goroutine
//
// In order: the grant (off, every name is `permanent-resolver-failure`, wasmtime 49.0.3's code, measured); an IP
// literal, answered as given; a syntactically invalid name, `invalid-argument`. All three resolve before the impl
// returns, as wasmtime answers them (name-lookup.reading's `run-sync` rows return). Anything else is a lookup on a
// goroutine, `net.Resolver.LookupNetIP`, counted in the instance's socket registry (`hostNet`), resolved through
// `onResolve`, and decided by one claim, E5's pattern (hostwait.go): the lookup's return, an async-lowered lookup's
// `subtask.cancel` (which cancels its context and resolves CANCELLED_BEFORE_RETURNED inline), or Close (which
// cancels it and resolves nothing).

// lookupErrorCodeLabels are `ip-name-lookup`'s own `error-code` cases, in order; the last, `other`, carries an
// `option<string>`.
var lookupErrorCodeLabels = []string{
	"access-denied", "invalid-argument", "name-unresolvable", "temporary-resolver-failure",
	"permanent-resolver-failure", "other",
}

func lookupErrorCodeType() canon.Type {
	other := canon.OptionType(canon.Type{Kind: canon.KindString})
	cases := make([]canon.Case, len(lookupErrorCodeLabels))
	for i, l := range lookupErrorCodeLabels {
		cases[i] = canon.Case{Name: l}
	}
	cases[len(cases)-1].Type = &other
	return canon.VariantType(cases...)
}

// ipAddressType is `ip-address`: `variant { ipv4(tuple<u8 ×4>), ipv6(tuple<u16 ×8>) }`.
func ipAddressType() canon.Type {
	v4 := canon.TupleType(u8T(), u8T(), u8T(), u8T())
	u := u16T()
	v6 := canon.TupleType(u, u, u, u, u, u, u, u)
	return canon.VariantType(canon.Case{Name: "ipv4", Type: &v4}, canon.Case{Name: "ipv6", Type: &v6})
}

// lookupResultType is `result<list<ip-address>, error-code>`.
func lookupResultType() canon.Type {
	elem := ipAddressType()
	l := canon.Type{Kind: canon.KindList, Elem: &elem}
	ec := lookupErrorCodeType()
	return canon.ResultType(&l, &ec)
}

// lookupErr is `err(code)`. `other` carries none: a string would be lowered through the guest's realloc, from the
// lookup's store, which the future payloads already avoid (ADR 0123's choice 10).
func lookupErr(label string) canon.Value {
	ec := lookupErrorCodeType()
	var code canon.Value
	var err error
	if label == "other" {
		none, nerr := canon.None(canon.OptionType(canon.Type{Kind: canon.KindString}))
		if nerr != nil {
			panic(nerr) // unreachable: a constant option type
		}
		code, err = canon.Variant(ec, "other", &none)
	} else {
		code, err = canon.Variant(ec, label, nil)
	}
	if err != nil {
		panic(err) // unreachable: every label passed is one of lookupErrorCodeLabels
	}
	v, err := canon.Variant(lookupResultType(), "error", &code)
	if err != nil {
		panic(err)
	}
	return v
}

// lookupOK is `ok(addrs)`.
func lookupOK(addrs []netip.Addr) (canon.Value, error) {
	elems := make([]canon.Value, len(addrs))
	for i, a := range addrs {
		v, err := ipAddressValue(a)
		if err != nil {
			return canon.Value{}, err
		}
		elems[i] = v
	}
	l, err := canon.List(ipAddressType(), elems...)
	if err != nil {
		return canon.Value{}, err
	}
	return canon.Variant(lookupResultType(), "ok", &l)
}

// ipAddressValue builds an `ip-address`. An IPv4-mapped address stays IPv6, as given.
func ipAddressValue(a netip.Addr) (canon.Value, error) {
	tuple := func(t canon.Type, vs ...canon.Value) (canon.Value, error) {
		m := make(map[string]canon.Value, len(vs))
		for i, v := range vs {
			m[strconv.Itoa(i)] = v
		}
		return canon.Record(t, m)
	}
	if a.Is4() {
		b := a.As4()
		t, err := tuple(canon.TupleType(u8T(), u8T(), u8T(), u8T()), canon.U8(b[0]), canon.U8(b[1]), canon.U8(b[2]), canon.U8(b[3]))
		if err != nil {
			return canon.Value{}, err
		}
		return canon.Variant(ipAddressType(), "ipv4", &t)
	}
	b := a.As16()
	ws := make([]canon.Value, 8)
	ts := make([]canon.Type, 8)
	for i := range 8 {
		ws[i], ts[i] = canon.U16(binary.BigEndian.Uint16(b[2*i:])), u16T()
	}
	t, err := tuple(canon.TupleType(ts...), ws...)
	if err != nil {
		return canon.Value{}, err
	}
	return canon.Variant(ipAddressType(), "ipv6", &t)
}

// nameLookup is one pending lookup, in its instance's registry until its lookup returns, the guest cancels it, or
// Close stops it: sockDial's shape, with no lend.
type nameLookup struct {
	h       *asyncHandles
	cancel  context.CancelFunc
	resolve func(canon.Value)
}

// resolveAddresses is `resolve-addresses: async func(name: string) -> result<list<ip-address>, error-code>`, its
// name's (ptr, len) the two flat parameters. The lookup runs on a goroutine of its own, the one lookup site,
// authorised by ADR 0126 in the census (TestEveryEngineGoroutineIsAtASiteADecisionAuthorises). It runs no guest
// code, and ends when LookupNetIP returns, which a cancel or Close makes prompt by cancelling its context.
//
// NEUTER: drop the grant check, and TestP3LookupMatchesWasmtime's lookup-denied rows resolve, and every one of
// TestNameLookupMatchesWasmtime's `off` rows fails.
func (h *Host) resolveAddresses(async *asyncHandles, res *hostResources) asyncLowerImpl {
	return func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
		args := onStart()
		if len(args) != 2 {
			return nil, fmt.Errorf("component: resolve-addresses: got %d flat parameters, want 2 (name)", len(args))
		}
		sv, err := canon.LoadStringFromRange(guestHeap{c}, int(uint32(args[0].Bits)), int(uint32(args[1].Bits)))
		if err != nil {
			return nil, &interp.Trap{Reason: fmt.Sprintf("resolve-addresses: lifting the name: %v", err)}
		}
		name, _ := sv.Str() // LoadStringFromRange returns a string, so the kind check cannot fail
		// NEUTER: answer access-denied here, and TestNameLookupMatchesWasmtime's `off` rows read 0x80, and
		// TestP3LookupMatchesWasmtime's lookup-denied rows print "PermissionDenied: Permission denied (os error 2)"
		// where wasmtime's EAI_FAIL prints "Non-recoverable error".
		if !h.AllowNameLookup {
			onResolve(lookupErr("permanent-resolver-failure"))
			return nil, nil
		}
		if a, ok := parseIPLiteral(name); ok {
			v, verr := lookupOK([]netip.Addr{a})
			if verr != nil {
				return nil, verr
			}
			onResolve(v)
			return nil, nil
		}
		if !validLookupName(name) {
			onResolve(lookupErr("invalid-argument"))
			return nil, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		l := &nameLookup{h: async, cancel: cancel, resolve: onResolve}
		ended, ok := res.net.startLookup(l)
		if !ok {
			cancel()
			onResolve(lookupErr("permanent-resolver-failure")) // Close has stopped the instance's sockets
			return nil, nil
		}
		async.mu.Lock()
		async.sockPending++ // a waker for the park bound until the lookup's resolution is stored
		async.mu.Unlock()
		lookup := h.lookup
		if lookup == nil {
			lookup = net.DefaultResolver.LookupNetIP
		}
		go func() {
			defer ended()
			addrs, lerr := lookup(ctx, "ip", name)
			l.finish(res.net.claimLookup(l), addrs, lerr)
		}()
		return l.cancelSubtask(res.net), nil
	}
}

// finish is the lookup's return. Unclaimed, the guest's cancel or Close resolved it already.
//
// NEUTER: skip the resolve, and TestNameLookupMatchesWasmtime's run(0) and run-async(0) granted rows, localhost's
// lookups through each lower, wait until the hang guard.
func (l *nameLookup) finish(claimed bool, addrs []netip.Addr, lerr error) {
	defer l.cancel()
	if !claimed {
		return
	}
	v := lookupAnswer(addrs, lerr)
	l.resolve(v)
	l.h.mu.Lock()
	l.h.sockPending--
	l.h.mu.Unlock()
}

// lookupAnswer is a lookup's result: its addresses unmapped (the WIT: "never returns IPv4-mapped IPv6
// addresses"), or its error's code. An empty answer is name-unresolvable ("never succeeds with 0 results").
func lookupAnswer(addrs []netip.Addr, lerr error) canon.Value {
	if lerr != nil {
		return lookupErr(lookupErrorLabel(lerr))
	}
	if len(addrs) == 0 {
		return lookupErr("name-unresolvable")
	}
	out := make([]netip.Addr, len(addrs))
	for i, a := range addrs {
		out[i] = a.Unmap()
	}
	v, err := lookupOK(out)
	if err != nil {
		return lookupErr("other") // unreachable: an address always builds
	}
	return v
}

// lookupErrorLabel maps a Go resolver error to `error-code` by the WIT's POSIX equivalents: not found is
// `name-unresolvable` (EAI_NONAME), a temporary failure or a timeout `temporary-resolver-failure` (EAI_AGAIN),
// any other DNS error `permanent-resolver-failure` (EAI_FAIL), and anything else `other`. No reading measures
// these against wasmtime: each needs a DNS server.
//
// NEUTER: map not-found to permanent-resolver-failure, and TestALookupErrorMapsByTheWITsEquivalents fails its
// not-found row.
func lookupErrorLabel(err error) string {
	var de *net.DNSError
	if errors.As(err, &de) {
		switch {
		case de.IsNotFound:
			return "name-unresolvable"
		case de.IsTemporary, de.IsTimeout:
			return "temporary-resolver-failure"
		default:
			return "permanent-resolver-failure"
		}
	}
	return "other"
}

// cancelSubtask is an async-lowered lookup's onCancel, sockDial.cancelSubtask's shape: it runs as host code on
// the canceller's goroutine, so its CANCELLED resolution is queued and applied before the canceller reads the
// subtask's state. A lookup that returned first has resolved it, and the cancel does nothing (#1009).
//
// NEUTER: return without claiming or resolving, and TestACancelledLookupResolvesCancelledAndItsGoroutineEnds's
// cancel reads BLOCKED.
func (l *nameLookup) cancelSubtask(n *hostNet) func() {
	return func() {
		if !n.claimLookup(l) {
			return
		}
		l.cancel()
		l.resolve(canon.NoResolution())
		l.h.mu.Lock()
		l.h.sockPending--
		l.h.mu.Unlock()
	}
}

// parseIPLiteral reads a name that is an IP address, as wasmtime 49.0.3 answers one inline (name-lookup.reading):
// an address with no zone, an IPv6 address in brackets, or the URL standard's IPv4 forms (`127.1`, `0x7f.1`),
// which wasmtime's `url::Host::parse` accepts. A zoned address is not a literal, and its `%` makes it invalid.
func parseIPLiteral(name string) (netip.Addr, bool) {
	if a, err := netip.ParseAddr(name); err == nil {
		return a, a.Zone() == ""
	}
	if inner, ok := strings.CutPrefix(name, "["); ok {
		if inner, ok = strings.CutSuffix(inner, "]"); ok {
			if a, err := netip.ParseAddr(inner); err == nil && a.Is6() && a.Zone() == "" {
				return a, true
			}
		}
		return netip.Addr{}, false
	}
	return whatwgIPv4(name)
}

// whatwgIPv4 is the URL standard's IPv4 parser, applied when a host "ends in a number": up to four parts, each
// decimal, octal with a leading 0, or hex with 0x, the last filling the bytes the others leave. It reports false
// for a name that does not end in a number, and for one that does and does not parse, which validLookupName then
// refuses.
func whatwgIPv4(name string) (netip.Addr, bool) {
	parts := strings.Split(name, ".")
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 || len(parts) > 4 {
		return netip.Addr{}, false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, ok := whatwgIPv4Number(p)
		if !ok {
			return netip.Addr{}, false
		}
		nums[i] = n
	}
	for _, n := range nums[:len(nums)-1] {
		if n > 255 {
			return netip.Addr{}, false
		}
	}
	last := nums[len(nums)-1]
	if last >= 1<<(8*(5-len(nums))) {
		return netip.Addr{}, false
	}
	v := last
	for i, n := range nums[:len(nums)-1] {
		v += n << (8 * (3 - i))
	}
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), true
}

// whatwgIPv4Number is the URL standard's IPv4 number parser.
func whatwgIPv4Number(p string) (uint64, bool) {
	if p == "" {
		return 0, false
	}
	base := 10
	switch {
	case len(p) >= 2 && (p[:2] == "0x" || p[:2] == "0X"):
		p, base = p[2:], 16
	case len(p) >= 2 && p[0] == '0':
		p, base = p[1:], 8
	}
	if p == "" {
		return 0, true
	}
	n, err := strconv.ParseUint(p, base, 64)
	if err != nil || n > 0xffffffff {
		return 0, false
	}
	return n, true
}

// endsInANumber is the URL standard's "ends in a number" checker: a host whose last label (ignoring one trailing
// dot) is all digits, or parses as an IPv4 number, is an IPv4 address or invalid, never a domain.
func endsInANumber(name string) bool {
	parts := strings.Split(name, ".")
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	_, ok := whatwgIPv4Number(last)
	return ok
}

// validLookupName reports whether a name is a domain the resolver may be asked about: not empty, ASCII, free of the
// URL standard's forbidden domain code points, and not one that ends in a number (which whatwgIPv4 has already
// failed to parse). A non-ASCII name would need IDNA, which the WIT asks for and Go's standard library does not
// have, so it is refused rather than sent unconverted (ADR 0126's recorded divergence); so is a percent sign, which
// the URL standard decodes first.
func validLookupName(name string) bool {
	if name == "" || endsInANumber(name) {
		return false
	}
	for i := range len(name) {
		b := name[i]
		if b <= 0x20 || b >= 0x7f || strings.IndexByte("#%/:<>?@[\\]^|", b) >= 0 {
			return false
		}
	}
	return true
}
