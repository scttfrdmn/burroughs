// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// WASI 0.3's outgoing HTTP (#1383's E2-out, ADR 0135): `wasi:http/types@0.3`'s four resources, `fields`, `request`,
// `request-options` and `response`, as host resources, and `wasi:http/client@0.3`'s `send` (hosthttpsend.go). The
// incoming half, `handler.handle` and a serve loop, is E2-in's.
//
// # The resources are host objects
//
// Every method is sync and is a resource method (`method`), marshalled through the codec at the caller's realloc.
// Four functions carry streams and futures: `request.new`, `response.new` and the two `consume-body`s. They are stream
// consumers, bound with both tables (`bindRes`), and their whole type is the host's declaration (`httpSignatures`),
// checked against the guest's at instantiate (walker.checkDeclaredSignature), since `unmodeledInSig` refuses a stream
// in a resource method's signature.
//
// # What the rules are read from
//
// Every rule a method answers by is wasmtime 49.0.3's, read row by row through p3http (testdata/p3http.reading):
// a field's name is lowercased and must be a token, a forbidden name is refused, a value's bytes are HTAB, 0x20–0x7E
// or 0x80–0xFF, and the order `copy-all` answers is the `http` crate's `HeaderMap`'s, which removes by swap.
//
// # Locks
//
// `asyncHandles.mu` → { `hostResources.mu`, `hostHTTP.mu` }, both leaves. A message's and a fields' own mutex is taken
// with none of those held, and nothing is taken under it.

// The four resource types.
var (
	httpFieldsResource   = newHostResource("wasi:http/types::fields", nil)
	httpRequestResource  = newHostResource("wasi:http/types::request", destroyHTTPRequest)
	httpOptionsResource  = newHostResource("wasi:http/types::request-options", nil)
	httpResponseResource = newHostResource("wasi:http/types::response", destroyHTTPResponse)
)

// ---- The WIT's types, built through the codec's constructors.

func strT() canon.Type    { return canon.Type{Kind: canon.KindString} }
func u64T() canon.Type    { return canon.Type{Kind: canon.KindU64} }
func optStrT() canon.Type { return canon.OptionType(strT()) }
func bytesT() canon.Type {
	return canon.Type{Kind: canon.KindList, Elem: &canon.Type{Kind: canon.KindU8}}
}
func valuesT() canon.Type  { return canon.Type{Kind: canon.KindList, Elem: ptrTo(bytesT())} }
func entryT() canon.Type   { return canon.TupleType(strT(), bytesT()) }
func entriesT() canon.Type { return canon.Type{Kind: canon.KindList, Elem: ptrTo(entryT())} }
func fieldsOwnT() canon.Type {
	return canon.OwnType(httpFieldsResource.rt)
}
func ptrTo(t canon.Type) *canon.Type { return &t }

// httpErrorCodeType is `error-code`, its 39 cases in the WIT's order, which is its discriminant.
func httpErrorCodeType() canon.Type {
	optStr, optU64, optU32 := optStrT(), canon.OptionType(u64T()), canon.OptionType(u32T())
	dns := canon.RecordType(canon.Field{Name: "rcode", Type: optStr}, canon.Field{Name: "info-code", Type: canon.OptionType(u16T())})
	alert := canon.RecordType(canon.Field{Name: "alert-id", Type: canon.OptionType(u8T())}, canon.Field{Name: "alert-message", Type: optStr})
	size := canon.RecordType(canon.Field{Name: "field-name", Type: optStr}, canon.Field{Name: "field-size", Type: optU32})
	optSize := canon.OptionType(size)
	c := func(name string, t *canon.Type) canon.Case { return canon.Case{Name: name, Type: t} }
	return canon.VariantType(
		c("DNS-timeout", nil), c("DNS-error", &dns), c("destination-not-found", nil), c("destination-unavailable", nil),
		c("destination-IP-prohibited", nil), c("destination-IP-unroutable", nil), c("connection-refused", nil),
		c("connection-terminated", nil), c("connection-timeout", nil), c("connection-read-timeout", nil),
		c("connection-write-timeout", nil), c("connection-limit-reached", nil), c("TLS-protocol-error", nil),
		c("TLS-certificate-error", nil), c("TLS-alert-received", &alert), c("HTTP-request-denied", nil),
		c("HTTP-request-length-required", nil), c("HTTP-request-body-size", &optU64), c("HTTP-request-method-invalid", nil),
		c("HTTP-request-URI-invalid", nil), c("HTTP-request-URI-too-long", nil), c("HTTP-request-header-section-size", &optU32),
		c("HTTP-request-header-size", &optSize), c("HTTP-request-trailer-section-size", &optU32),
		c("HTTP-request-trailer-size", &size), c("HTTP-response-incomplete", nil),
		c("HTTP-response-header-section-size", &optU32), c("HTTP-response-header-size", &size),
		c("HTTP-response-body-size", &optU64), c("HTTP-response-trailer-section-size", &optU32),
		c("HTTP-response-trailer-size", &size), c("HTTP-response-transfer-coding", &optStr),
		c("HTTP-response-content-coding", &optStr), c("HTTP-response-timeout", nil), c("HTTP-upgrade-failed", nil),
		c("HTTP-protocol-error", nil), c("loop-detected", nil), c("configuration-error", nil), c("internal-error", &optStr),
	)
}

// headerErrorType is `header-error`, and requestOptionsErrorType `request-options-error`.
func headerErrorType() canon.Type {
	o := optStrT()
	return canon.VariantType(canon.Case{Name: "invalid-syntax"}, canon.Case{Name: "forbidden"}, canon.Case{Name: "immutable"},
		canon.Case{Name: "size-exceeded"}, canon.Case{Name: "other", Type: &o})
}

func requestOptionsErrorType() canon.Type {
	o := optStrT()
	return canon.VariantType(canon.Case{Name: "not-supported"}, canon.Case{Name: "immutable"}, canon.Case{Name: "other", Type: &o})
}

// httpMethodLabels are `method`'s cases but its last, `other(string)`.
var httpMethodLabels = []string{"get", "head", "post", "put", "delete", "connect", "options", "trace", "patch"}

// httpStandardMethods maps each standard method's wire spelling to its case, so an `other` that spells one exactly is
// that method.
var httpStandardMethods = func() map[string]string {
	m := make(map[string]string, len(httpMethodLabels))
	for _, l := range httpMethodLabels {
		m[strings.ToUpper(l)] = l
	}
	return m
}()

func httpMethodType() canon.Type {
	cases := make([]canon.Case, 0, len(httpMethodLabels)+1)
	for _, l := range httpMethodLabels {
		cases = append(cases, canon.Case{Name: l})
	}
	s := strT()
	return canon.VariantType(append(cases, canon.Case{Name: "other", Type: &s})...)
}

func httpSchemeType() canon.Type {
	s := strT()
	return canon.VariantType(canon.Case{Name: "HTTP"}, canon.Case{Name: "HTTPS"}, canon.Case{Name: "other", Type: &s})
}

// trailersPayloadType is the trailers future's payload, `result<option<trailers>, error-code>`, and transmitPayloadType
// a transmission future's and `res`'s, `result<_, error-code>`.
func trailersPayloadType() canon.Type {
	o, ec := canon.OptionType(fieldsOwnT()), httpErrorCodeType()
	return canon.ResultType(&o, &ec)
}

func transmitPayloadType() canon.Type {
	ec := httpErrorCodeType()
	return canon.ResultType(nil, &ec)
}

// emptyOf is t with every option none, for the payload of an error-code case the host answers: no case it builds
// carries a string, so no realloc runs in the store that lowers it (ADR 0135).
func emptyOf(t canon.Type) (canon.Value, error) {
	switch t.Kind {
	case canon.KindOption:
		return canon.None(t)
	case canon.KindRecord:
		m := make(map[string]canon.Value, len(t.Fields))
		for _, f := range t.Fields {
			v, err := emptyOf(f.Type)
			if err != nil {
				return canon.Value{}, err
			}
			m[f.Name] = v
		}
		return canon.Record(t, m)
	default:
		return canon.Value{}, fmt.Errorf("component: http: no empty value of %s", t)
	}
}

// httpErrorCode is error-code's case `label`, its payload empty.
func httpErrorCode(label string) canon.Value {
	ec := httpErrorCodeType()
	for _, c := range ec.Cases {
		if c.Name != label {
			continue
		}
		var payload *canon.Value
		if c.Type != nil {
			p, err := emptyOf(*c.Type)
			if err != nil {
				panic(err) // unreachable: every payload of error-code is an option or a record of options
			}
			payload = &p
		}
		v, err := canon.Variant(ec, label, payload)
		if err != nil {
			panic(err)
		}
		return v
	}
	panic("component: http: error-code has no case " + label)
}

// httpResultErr is `result::err(code)` at `result<ok, error-code>`, and httpResultOK `result::ok(v)`.
func httpResultErr(ok *canon.Type, label string) canon.Value {
	ec := httpErrorCodeType()
	code := httpErrorCode(label)
	v, err := canon.Variant(canon.ResultType(ok, &ec), "error", &code)
	if err != nil {
		panic(err)
	}
	return v
}

func httpResultOK(ok *canon.Type, payload *canon.Value) canon.Value {
	ec := httpErrorCodeType()
	v, err := canon.Variant(canon.ResultType(ok, &ec), "ok", payload)
	if err != nil {
		panic(err)
	}
	return v
}

// headerResult is `result<ok, header-error>`: label "" is ok(payload), anything else that error's case.
func headerResult(ok *canon.Type, label string, payload *canon.Value) (canon.Value, error) {
	he := headerErrorType()
	t := canon.ResultType(ok, &he)
	if label == "" {
		return canon.Variant(t, "ok", payload)
	}
	code, err := canon.Variant(he, label, nil)
	if err != nil {
		return canon.Value{}, err
	}
	return canon.Variant(t, "error", &code)
}

// elemOfCanon is the element descriptor of a host-minted future of type t, elemOfPayload's for a type rather than a
// value (#1282 item 1): a transmission future is minted before its value is known.
func elemOfCanon(t canon.Type) *elemType {
	return &elemType{known: true, modeled: true, t: &t, size: canon.ElemSize(t), align: canon.Alignment(t)}
}

// ---- fields

// fieldGroup is one name's values, in a fields' order.
type fieldGroup struct {
	name   string
	values [][]byte
}

// httpFields is the object behind a `fields` handle: the `http` crate's `HeaderMap` as wasmtime 49.0.3 answers it,
// groups of a lowercased name's values in first-insertion order, a set replacing a name's values in place and a
// removal moving the last group into the removed one's place. Its own mutex guards it, since two agents of a shared
// memory may call its methods at once.
type httpFields struct {
	mu        sync.Mutex
	groups    []fieldGroup
	immutable bool
}

func (f *httpFields) indexLocked(name string) int {
	for i := range f.groups {
		if f.groups[i].name == name {
			return i
		}
	}
	return -1
}

func (f *httpFields) appendLocked(name string, v []byte) {
	v = storedValue(name, v)
	if i := f.indexLocked(name); i >= 0 {
		f.groups[i].values = append(f.groups[i].values, append([]byte(nil), v...))
		return
	}
	f.groups = append(f.groups, fieldGroup{name: name, values: [][]byte{append([]byte(nil), v...)}})
}

// removeLocked is HeaderMap's swap-remove.
//
// NEUTER, run: remove in order instead, and TestP3HTTPMatchesWasmtime's order row (and only it) reads [b, b, c, d]
// after `delete a` where wasmtime reads [d, b, b, c].
func (f *httpFields) removeLocked(i int) {
	last := len(f.groups) - 1
	f.groups[i] = f.groups[last]
	f.groups = f.groups[:last]
}

// copyOf is a deep copy, immutable or not.
func (f *httpFields) copyOf(immutable bool) *httpFields {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &httpFields{immutable: immutable, groups: make([]fieldGroup, len(f.groups))}
	for i, g := range f.groups {
		vs := make([][]byte, len(g.values))
		for j, v := range g.values {
			vs[j] = append([]byte(nil), v...)
		}
		c.groups[i] = fieldGroup{name: g.name, values: vs}
	}
	return c
}

// entries is copy-all's answer: every value with its name, in order.
func (f *httpFields) entries() []fieldGroup {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fieldGroup
	for _, g := range f.groups {
		for _, v := range g.values {
			out = append(out, fieldGroup{name: g.name, values: [][]byte{append([]byte(nil), v...)}})
		}
	}
	return out
}

// httpTokenByte is RFC 9110's tchar.
func httpTokenByte(b byte) bool {
	switch {
	case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", b) >= 0
}

func httpToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !httpTokenByte(s[i]) {
			return false
		}
	}
	return true
}

// httpForbiddenFields are the names wasmtime 49.0.3 refuses with `forbidden`, each read on p3http's fields row: the
// connection's own, the host and HTTP/2's settings, and the proxy's credentials. `te` is not among them.
var httpForbiddenFields = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-connection": true, "transfer-encoding": true, "upgrade": true,
	"host": true, "http2-settings": true, "proxy-authorization": true, "proxy-authenticate": true,
}

// validFieldValue is the `http` crate's HeaderValue rule, HTAB, 0x20–0x7E and 0x80–0xFF, and a `content-length`'s
// digits, as wasmtime reads them.
//
// NEUTER, run: accept every value, and TestP3HTTPMatchesWasmtime's fields row (and only it) appends the LF, NUL, CR and
// DEL values and every content-length, "", "1,1" and "-1" among them, and sets "ok" with its LF.
func validFieldValue(name string, v []byte) bool {
	for _, b := range v {
		if b != '\t' && (b < 0x20 || b == 0x7f) {
			return false
		}
	}
	if name == "content-length" {
		if len(v) == 0 {
			return false
		}
		for _, b := range v {
			if b < '0' || b > '9' {
				return false
			}
		}
		if _, err := strconv.ParseUint(string(v), 10, 64); err != nil {
			return false
		}
	}
	return true
}

// storedValue is the value a fields keeps: a `content-length` is re-rendered as its number, as wasmtime 49.0.3 reads
// "08" back as "8" (p3http's fields row), and anything else is kept as written. The caller checked it with
// validFieldValue.
//
// NEUTER, run: keep every value as written, and TestP3HTTPMatchesWasmtime's fields row (and only it) reads
// ("content-length", [48, 56]) in its clone's copy-all where wasmtime reads [56].
func storedValue(name string, v []byte) []byte {
	if name != "content-length" {
		return v
	}
	n, err := strconv.ParseUint(string(v), 10, 64)
	if err != nil {
		return v // unreachable: validFieldValue parsed it
	}
	return []byte(strconv.FormatUint(n, 10))
}

// fieldCheck is a mutation's `header-error`, "" for none, in the order wasmtime checks: the name's syntax, then
// forbidden, then each value's syntax, then immutability.
func fieldCheck(f *httpFields, name string, values [][]byte) string {
	if !httpToken(name) {
		return "invalid-syntax"
	}
	if httpForbiddenFields[strings.ToLower(name)] {
		return "forbidden"
	}
	for _, v := range values {
		if !validFieldValue(strings.ToLower(name), v) {
			return "invalid-syntax"
		}
	}
	// NEUTER, run: drop the immutability check, and twelve of TestP3HTTPMatchesWasmtime's rows fail (get, head, purge,
	// dump, the five posts that reach a response, bigresp, request and response): each sets, appends to or deletes from
	// a request's or response's headers or a response's trailers, "headers set: Ok(())" where wasmtime reads Immutable.
	if f.immutable {
		return "immutable"
	}
	return ""
}

// ---- Lifting and lowering at the ABI.

// liftString lifts a (ptr, len) string, trapping as the codec refuses it.
func liftString(c *interp.CanonCaller, op string, ptr, n interp.Value) (string, error) {
	sv, err := canon.LoadStringFromRange(guestHeap{c}, int(uint32(ptr.Bits)), int(uint32(n.Bits)))
	if err != nil {
		return "", &interp.Trap{Reason: fmt.Sprintf("%s: lifting a string: %v", op, err)}
	}
	s, _ := sv.Str() // LoadStringFromRange returns a string, so the kind check cannot fail
	return s, nil
}

func liftBytes(c *interp.CanonCaller, op string, ptr, n uint32) ([]byte, error) {
	b, err := canon.LoadListU8(guestHeap{c}, int(ptr), int(n))
	if err != nil {
		return nil, &interp.Trap{Reason: fmt.Sprintf("%s: lifting a list<u8>: %v", op, err)}
	}
	return b, nil
}

// liftListHeaders reads a list of n elements of `size` bytes at ptr, checked as def:load_list_from_valid_range
// checks it: aligned to 4, and in memory.
func liftListHeaders(c *interp.CanonCaller, op string, ptr, n uint32, size int) ([]byte, error) {
	if ptr%4 != 0 {
		return nil, &interp.Trap{Reason: fmt.Sprintf("%s: a list at %d is not aligned to 4", op, ptr)}
	}
	nb, err := canon.ListByteLength(int(n), size)
	if err != nil {
		return nil, &interp.Trap{Reason: fmt.Sprintf("%s: %v", op, err)}
	}
	b, err := guestHeap{c}.ReadBytes(int(ptr), nb)
	if err != nil {
		return nil, &interp.Trap{Reason: fmt.Sprintf("%s: lifting a list: %v", op, err)}
	}
	return b, nil
}

// liftValues lifts a `list<field-value>`.
func liftValues(c *interp.CanonCaller, op string, ptr, n uint32) ([][]byte, error) {
	hdr, err := liftListHeaders(c, op, ptr, n, 8)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, n)
	for i := range out {
		p, l := binary.LittleEndian.Uint32(hdr[8*i:]), binary.LittleEndian.Uint32(hdr[8*i+4:])
		if out[i], err = liftBytes(c, op, p, l); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// liftEntries lifts a `list<tuple<field-name, field-value>>`.
func liftEntries(c *interp.CanonCaller, op string, ptr, n uint32) ([]fieldGroup, error) {
	hdr, err := liftListHeaders(c, op, ptr, n, 16)
	if err != nil {
		return nil, err
	}
	out := make([]fieldGroup, n)
	for i := range out {
		w := func(k int) interp.Value { return interp.I32(int32(binary.LittleEndian.Uint32(hdr[16*i+4*k:]))) }
		name, err := liftString(c, op, w(0), w(1))
		if err != nil {
			return nil, err
		}
		v, err := liftBytes(c, op, uint32(w(2).Bits), uint32(w(3).Bits))
		if err != nil {
			return nil, err
		}
		out[i] = fieldGroup{name: name, values: [][]byte{v}}
	}
	return out, nil
}

func bytesValue(b []byte) canon.Value {
	us := make([]canon.Value, len(b))
	for i, x := range b {
		us[i] = canon.U8(x)
	}
	v, err := canon.List(canon.Type{Kind: canon.KindU8}, us...)
	if err != nil {
		panic(err) // unreachable: every element is a u8
	}
	return v
}

func valuesValue(vs [][]byte) canon.Value {
	els := make([]canon.Value, len(vs))
	for i, b := range vs {
		els[i] = bytesValue(b)
	}
	v, err := canon.List(bytesT(), els...)
	if err != nil {
		panic(err)
	}
	return v
}

func entriesValue(es []fieldGroup) (canon.Value, error) {
	els := make([]canon.Value, len(es))
	for i, e := range es {
		v, err := canon.Record(entryT(), map[string]canon.Value{"0": canon.Str(e.name), "1": bytesValue(e.values[0])})
		if err != nil {
			return canon.Value{}, err
		}
		els[i] = v
	}
	return canon.List(entryT(), els...)
}

func optionValue(t canon.Type, v *canon.Value) canon.Value {
	ot := canon.OptionType(t)
	var out canon.Value
	var err error
	if v == nil {
		out, err = canon.None(ot)
	} else {
		out, err = canon.Some(ot, *v)
	}
	if err != nil {
		panic(err)
	}
	return out
}

// resultFlat is a payload-free `result`'s flat word: 0 ok, 1 error.
func resultFlat(ok bool) []interp.Value {
	if ok {
		return []interp.Value{interp.I32(0)}
	}
	return []interp.Value{interp.I32(1)}
}

func store(c *interp.CanonCaller, ret interp.Value, v canon.Value, err error) ([]interp.Value, error) {
	return nil, lowerResult(c, uint64(uint32(ret.Bits)), v, err)
}

func wantArgs(op string, args []interp.Value, n int, shape string) error {
	if len(args) != n {
		return fmt.Errorf("component: %s: got %d core args, want %d (%s)", op, len(args), n, shape)
	}
	return nil
}

// ---- fields' methods.

func (h *Host) fieldsNew(res *hostResources) interp.CanonFunc {
	return func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		return []interp.Value{interp.I32(res.mint(httpFieldsResource, &httpFields{}))}, nil
	}
}

// fieldsFromList is `[static]fields.from-list: func(entries) -> result<fields, header-error>`, (ptr, len, ret).
func (h *Host) fieldsFromList(res *hostResources) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if err := wantArgs("fields.from-list", args, 3, "ptr, len, ret"); err != nil {
			return nil, err
		}
		es, err := liftEntries(c, "fields.from-list", uint32(args[0].Bits), uint32(args[1].Bits))
		if err != nil {
			return nil, err
		}
		own := fieldsOwnT()
		f := &httpFields{}
		for _, e := range es {
			if label := fieldCheck(f, e.name, e.values); label != "" {
				return storeAt(c, args[2])(headerResult(&own, label, nil))
			}
			f.appendLocked(strings.ToLower(e.name), e.values[0])
		}
		handle := canon.Own(httpFieldsResource.rt, uint32(res.mint(httpFieldsResource, f)))
		return storeAt(c, args[2])(headerResult(&own, "", &handle))
	}
}

// store's curried form, for a result built with its error.
func storeAt(c *interp.CanonCaller, ret interp.Value) func(canon.Value, error) ([]interp.Value, error) {
	return func(v canon.Value, err error) ([]interp.Value, error) { return store(c, ret, v, err) }
}

func (h *Host) fieldsGet(_ *hostResources, c *interp.CanonCaller, f *httpFields, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("fields.get", rest, 3, "name ptr, len, ret"); err != nil {
		return nil, err
	}
	name, err := liftString(c, "fields.get", rest[0], rest[1])
	if err != nil {
		return nil, err
	}
	var vs [][]byte
	if httpToken(name) {
		f.mu.Lock()
		if i := f.indexLocked(strings.ToLower(name)); i >= 0 {
			vs = append(vs, f.groups[i].values...)
		}
		f.mu.Unlock()
	}
	return store(c, rest[2], valuesValue(vs), nil)
}

func (h *Host) fieldsHas(_ *hostResources, c *interp.CanonCaller, f *httpFields, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("fields.has", rest, 2, "name ptr, len"); err != nil {
		return nil, err
	}
	name, err := liftString(c, "fields.has", rest[0], rest[1])
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	has := httpToken(name) && f.indexLocked(strings.ToLower(name)) >= 0
	f.mu.Unlock()
	if has {
		return []interp.Value{interp.I32(1)}, nil
	}
	return []interp.Value{interp.I32(0)}, nil
}

// fieldsSet is `set: func(name, value: list<field-value>) -> result<_, header-error>`. An empty list removes the name.
func (h *Host) fieldsSet(_ *hostResources, c *interp.CanonCaller, f *httpFields, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("fields.set", rest, 5, "name ptr, len, values ptr, len, ret"); err != nil {
		return nil, err
	}
	name, err := liftString(c, "fields.set", rest[0], rest[1])
	if err != nil {
		return nil, err
	}
	vs, err := liftValues(c, "fields.set", uint32(rest[2].Bits), uint32(rest[3].Bits))
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	label := fieldCheck(f, name, vs)
	if label == "" {
		lower := strings.ToLower(name)
		for j := range vs {
			vs[j] = storedValue(lower, vs[j])
		}
		i := f.indexLocked(lower)
		switch {
		case i >= 0 && len(vs) == 0:
			f.removeLocked(i)
		case i >= 0:
			f.groups[i].values = vs // set replaces in place (HeaderMap's insert)
		case len(vs) > 0:
			f.groups = append(f.groups, fieldGroup{name: lower, values: vs})
		}
	}
	f.mu.Unlock()
	return storeAt(c, rest[4])(headerResult(nil, label, nil))
}

func (h *Host) fieldsDelete(_ *hostResources, c *interp.CanonCaller, f *httpFields, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("fields.delete", rest, 3, "name ptr, len, ret"); err != nil {
		return nil, err
	}
	name, err := liftString(c, "fields.delete", rest[0], rest[1])
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	label := fieldCheck(f, name, nil)
	if label == "" {
		if i := f.indexLocked(strings.ToLower(name)); i >= 0 {
			f.removeLocked(i)
		}
	}
	f.mu.Unlock()
	return storeAt(c, rest[2])(headerResult(nil, label, nil))
}

func (h *Host) fieldsGetAndDelete(_ *hostResources, c *interp.CanonCaller, f *httpFields, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("fields.get-and-delete", rest, 3, "name ptr, len, ret"); err != nil {
		return nil, err
	}
	name, err := liftString(c, "fields.get-and-delete", rest[0], rest[1])
	if err != nil {
		return nil, err
	}
	var vs [][]byte
	f.mu.Lock()
	label := fieldCheck(f, name, nil)
	if label == "" {
		if i := f.indexLocked(strings.ToLower(name)); i >= 0 {
			vs = f.groups[i].values
			f.removeLocked(i)
		}
	}
	f.mu.Unlock()
	vt := valuesT()
	if label != "" {
		return storeAt(c, rest[2])(headerResult(&vt, label, nil))
	}
	v := valuesValue(vs)
	return storeAt(c, rest[2])(headerResult(&vt, "", &v))
}

func (h *Host) fieldsAppend(_ *hostResources, c *interp.CanonCaller, f *httpFields, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("fields.append", rest, 5, "name ptr, len, value ptr, len, ret"); err != nil {
		return nil, err
	}
	name, err := liftString(c, "fields.append", rest[0], rest[1])
	if err != nil {
		return nil, err
	}
	v, err := liftBytes(c, "fields.append", uint32(rest[2].Bits), uint32(rest[3].Bits))
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	label := fieldCheck(f, name, [][]byte{v})
	if label == "" {
		// NEUTER, run: keep the name's case, and TestP3HTTPMatchesWasmtime's fields row (and only it) reads ("A", [50]) as
		// a group of its own, and `get a` as [[49]], where wasmtime lowercases it into a's group.
		f.appendLocked(strings.ToLower(name), v)
	}
	f.mu.Unlock()
	return storeAt(c, rest[4])(headerResult(nil, label, nil))
}

func (h *Host) fieldsCopyAll(_ *hostResources, c *interp.CanonCaller, f *httpFields, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("fields.copy-all", rest, 1, "ret"); err != nil {
		return nil, err
	}
	return storeAt(c, rest[0])(entriesValue(f.entries()))
}

// fieldsClone is `clone: func() -> fields`: a deep copy, mutable whatever this one is.
func (h *Host) fieldsClone(res *hostResources, _ *interp.CanonCaller, f *httpFields, _ []interp.Value) ([]interp.Value, error) {
	return []interp.Value{interp.I32(res.mint(httpFieldsResource, f.copyOf(false)))}, nil
}

// ---- request-options

// httpOptions is the object behind a `request-options` handle: three optional timeouts in nanoseconds. All three
// are honoured by send, so none is `not-supported`, as none is on wasmtime 49.0.3 (p3http's options row).
type httpOptions struct {
	mu        sync.Mutex
	timeouts  [3]*uint64 // connect, first-byte, between-bytes
	immutable bool
}

func (o *httpOptions) copyOf(immutable bool) *httpOptions {
	o.mu.Lock()
	defer o.mu.Unlock()
	c := &httpOptions{immutable: immutable}
	for i, t := range o.timeouts {
		if t != nil {
			v := *t
			c.timeouts[i] = &v
		}
	}
	return c
}

func (h *Host) optionsNew(res *hostResources) interp.CanonFunc {
	return func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		return []interp.Value{interp.I32(res.mint(httpOptionsResource, &httpOptions{}))}, nil
	}
}

// optionsGet is a timeout's getter: `func() -> option<duration>`, (self, ret).
func optionsGet(which int) func(*hostResources, *interp.CanonCaller, *httpOptions, []interp.Value) ([]interp.Value, error) {
	return func(_ *hostResources, c *interp.CanonCaller, o *httpOptions, rest []interp.Value) ([]interp.Value, error) {
		if err := wantArgs("request-options get", rest, 1, "ret"); err != nil {
			return nil, err
		}
		o.mu.Lock()
		t := o.timeouts[which]
		o.mu.Unlock()
		var v *canon.Value
		if t != nil {
			u := canon.U64(*t)
			v = &u
		}
		return store(c, rest[0], optionValue(u64T(), v), nil)
	}
}

// optionsSet is a timeout's setter: `func(duration: option<duration>) -> result<_, request-options-error>`, (self,
// disc, nanoseconds, ret). An immutable one, from get-options, is `immutable`.
func optionsSet(which int) func(*hostResources, *interp.CanonCaller, *httpOptions, []interp.Value) ([]interp.Value, error) {
	return func(_ *hostResources, c *interp.CanonCaller, o *httpOptions, rest []interp.Value) ([]interp.Value, error) {
		if err := wantArgs("request-options set", rest, 3, "disc, duration, ret"); err != nil {
			return nil, err
		}
		disc := uint32(rest[0].Bits)
		if disc > 1 {
			return nil, &interp.Trap{Reason: fmt.Sprintf("request-options set: option case %d out of range", disc)}
		}
		re := requestOptionsErrorType()
		t := canon.ResultType(nil, &re)
		o.mu.Lock()
		immutable := o.immutable
		if !immutable {
			o.timeouts[which] = nil
			if disc == 1 {
				v := rest[1].Bits
				o.timeouts[which] = &v
			}
		}
		o.mu.Unlock()
		// NEUTER, run: drop the immutability check, and TestP3HTTPMatchesWasmtime's options row (and only it) reads "got
		// set: Ok(())" where wasmtime reads Immutable. Its second get-options still reads 2s, since each is a copy.
		if immutable {
			code, err := canon.Variant(re, "immutable", nil)
			if err != nil {
				return nil, err
			}
			return storeAt(c, rest[2])(canon.Variant(t, "error", &code))
		}
		return storeAt(c, rest[2])(canon.Variant(t, "ok", nil))
	}
}

func (h *Host) optionsClone(res *hostResources, _ *interp.CanonCaller, o *httpOptions, _ []interp.Value) ([]interp.Value, error) {
	return []interp.Value{interp.I32(res.mint(httpOptionsResource, o.copyOf(false)))}, nil
}

// ---- request

// httpCase is a `method` or `scheme` value: its case's label, and other's string.
type httpCase struct {
	label, other string
}

// httpGuestBody is a message's body as the guest gave it to new: its stream, nil for none, and its trailers future.
// Both stay in the guest's table (liftStreamArgLocked's divergence), so consume-body can hand them back.
type httpGuestBody struct {
	contents *readableStreamEnd
	trailers *readableFutureEnd
}

// httpRequest is the object behind a `request` handle. Its fields are guarded by mu; body and sent are guarded by
// asyncHandles.mu, as the ends they hold are. incoming is set for a request the host minted from one it is serving
// (hostserve.go, ADR 0137), whose body is the host's rather than the guest's; it is never written after the mint.
type httpRequest struct {
	mu        sync.Mutex
	method    httpCase
	path      *string
	scheme    *httpCase
	authority *string
	headers   *httpFields
	options   *httpOptions
	h         *asyncHandles
	reg       *hostHTTP
	body      httpGuestBody
	sent      *readableFutureEnd // new's transmission future
	incoming  *httpIncoming
}

// methodValue and schemeValue are a case's value.
func caseValue(t canon.Type, c httpCase) canon.Value {
	var p *canon.Value
	if c.label == "other" {
		s := canon.Str(c.other)
		p = &s
	}
	v, err := canon.Variant(t, c.label, p)
	if err != nil {
		panic(err)
	}
	return v
}

// liftCase lifts a variant of payload-free cases and a last `other(string)` from its flat words (disc, ptr, len).
func liftCase(c *interp.CanonCaller, op string, labels []string, words []interp.Value) (httpCase, error) {
	disc := uint32(words[0].Bits)
	switch {
	case disc < uint32(len(labels)):
		return httpCase{label: labels[disc]}, nil
	case disc == uint32(len(labels)):
		s, err := liftString(c, op, words[1], words[2])
		return httpCase{label: "other", other: s}, err
	default:
		return httpCase{}, &interp.Trap{Reason: fmt.Sprintf("%s: case %d out of range", op, disc)}
	}
}

// requestMethod reads a lent request's fields under its lock.
func (r *httpRequest) locked(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f()
}

func (h *Host) requestGetMethod(_ *hostResources, c *interp.CanonCaller, r *httpRequest, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("request.get-method", rest, 1, "ret"); err != nil {
		return nil, err
	}
	var m httpCase
	r.locked(func() { m = r.method })
	return store(c, rest[0], caseValue(httpMethodType(), m), nil)
}

// requestSetMethod is `set-method: func(method) -> result`, (self, disc, ptr, len) -> result. An `other` must be a
// token, and one that spells a standard method exactly is that method (p3http's request row: "GET" reads back get,
// "get" stays other).
func (h *Host) requestSetMethod(_ *hostResources, c *interp.CanonCaller, r *httpRequest, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("request.set-method", rest, 3, "disc, ptr, len"); err != nil {
		return nil, err
	}
	m, err := liftCase(c, "request.set-method", httpMethodLabels, rest)
	if err != nil {
		return nil, err
	}
	if m.label == "other" {
		if !httpToken(m.other) {
			return resultFlat(false), nil
		}
		// Case-sensitive, as methods are: "GET" is get, and "get" stays other.
		if l, ok := httpStandardMethods[m.other]; ok {
			m = httpCase{label: l}
		}
	}
	r.locked(func() { r.method = m })
	return resultFlat(true), nil
}

// liftOptString lifts an `option<string>` from its flat words (disc, ptr, len).
func liftOptString(c *interp.CanonCaller, op string, words []interp.Value) (*string, error) {
	switch disc := uint32(words[0].Bits); disc {
	case 0:
		return nil, nil
	case 1:
		s, err := liftString(c, op, words[1], words[2])
		return &s, err
	default:
		return nil, &interp.Trap{Reason: fmt.Sprintf("%s: option case %d out of range", op, disc)}
	}
}

func optStrValue(s *string) canon.Value {
	if s == nil {
		return optionValue(strT(), nil)
	}
	v := canon.Str(*s)
	return optionValue(strT(), &v)
}

func (h *Host) requestGetPath(_ *hostResources, c *interp.CanonCaller, r *httpRequest, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("request.get-path-with-query", rest, 1, "ret"); err != nil {
		return nil, err
	}
	var p *string
	r.locked(func() { p = r.path })
	return store(c, rest[0], optStrValue(p), nil)
}

// validPathWithQuery is the `http` crate's PathAndQuery, as wasmtime 49.0.3 reads it on p3http's request row: "" is
// "/", anything else begins with "/", a fragment is dropped, and the path's and the query's bytes are the crate's
// classes, 0x80 and above included. It answers the path the request stores.
//
// NEUTER, run: accept every path as written, and TestP3HTTPMatchesWasmtime's request row (and only it) reads "/a b",
// "a", "*" and "/?q=<" as Ok, "" as "" where wasmtime reads "/", and "/a#f" with its fragment.
func validPathWithQuery(s string) (string, bool) {
	if s == "" {
		return "/", true
	}
	if s[0] != '/' {
		return "", false
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	inQuery := false
	for i := range len(s) {
		b := s[i]
		switch {
		case b >= 0x80:
		case !inQuery && b == '?':
			inQuery = true
		case inQuery && (b == 0x21 || (0x24 <= b && b <= 0x3b) || b == 0x3d || (0x3f <= b && b <= 0x7e)):
		case !inQuery && (b == 0x21 || b == '"' || (0x24 <= b && b <= 0x3b) || b == 0x3d || (0x40 <= b && b <= 0x5f) ||
			(0x61 <= b && b <= 0x7e)):
		default:
			return "", false
		}
	}
	return s, true
}

func (h *Host) requestSetPath(_ *hostResources, c *interp.CanonCaller, r *httpRequest, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("request.set-path-with-query", rest, 3, "disc, ptr, len"); err != nil {
		return nil, err
	}
	p, err := liftOptString(c, "request.set-path-with-query", rest)
	if err != nil {
		return nil, err
	}
	if p != nil {
		s, ok := validPathWithQuery(*p)
		if !ok {
			return resultFlat(false), nil
		}
		p = &s
	}
	r.locked(func() { r.path = p })
	return resultFlat(true), nil
}

var httpSchemeLabels = []string{"HTTP", "HTTPS"}

func (h *Host) requestGetScheme(_ *hostResources, c *interp.CanonCaller, r *httpRequest, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("request.get-scheme", rest, 1, "ret"); err != nil {
		return nil, err
	}
	var s *httpCase
	r.locked(func() { s = r.scheme })
	if s == nil {
		return store(c, rest[0], optionValue(httpSchemeType(), nil), nil)
	}
	v := caseValue(httpSchemeType(), *s)
	return store(c, rest[0], optionValue(httpSchemeType(), &v), nil)
}

// validScheme is the `http` crate's scheme rule as wasmtime reads it: letters, digits, "+", "-" and ".", at most 64,
// and "1x" is accepted.
func validScheme(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := range len(s) {
		if !schemeByte(s[i]) {
			return false
		}
	}
	return true
}

func schemeByte(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '+' || b == '-' || b == '.'
}

// requestSetScheme is `set-scheme: func(option<scheme>) -> result`, (self, odisc, sdisc, ptr, len). An `other`
// spelling "http" or "https" exactly is that scheme; "HTTPS" stays other, as wasmtime reads it.
func (h *Host) requestSetScheme(_ *hostResources, c *interp.CanonCaller, r *httpRequest, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("request.set-scheme", rest, 4, "odisc, sdisc, ptr, len"); err != nil {
		return nil, err
	}
	var s *httpCase
	switch disc := uint32(rest[0].Bits); disc {
	case 0:
	case 1:
		v, err := liftCase(c, "request.set-scheme", httpSchemeLabels, rest[1:])
		if err != nil {
			return nil, err
		}
		if v.label == "other" {
			if !validScheme(v.other) {
				return resultFlat(false), nil
			}
			switch v.other {
			case "http":
				v = httpCase{label: "HTTP"}
			case "https":
				v = httpCase{label: "HTTPS"}
			}
		}
		s = &v
	default:
		return nil, &interp.Trap{Reason: fmt.Sprintf("request.set-scheme: option case %d out of range", disc)}
	}
	r.locked(func() { r.scheme = s })
	return resultFlat(true), nil
}

func (h *Host) requestGetAuthority(_ *hostResources, c *interp.CanonCaller, r *httpRequest, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("request.get-authority", rest, 1, "ret"); err != nil {
		return nil, err
	}
	var a *string
	r.locked(func() { a = r.authority })
	return store(c, rest[0], optStrValue(a), nil)
}

// validAuthority is the `http` crate's Authority rule as wasmtime reads it: non-empty; userinfo, a bracketed IPv6
// host and a port allowed; a port, if a colon follows the host, of digits and non-empty; no "/", "?", "#" or space.
func validAuthority(s string) bool {
	if s == "" {
		return false
	}
	host := s
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		host = s[i+1:]
		if strings.IndexByte(s[:i], '@') >= 0 {
			return false
		}
	}
	for i := range len(s) {
		b := s[i]
		if b <= 0x20 || b >= 0x7f || strings.IndexByte("/?#\"<>\\^`{|}", b) >= 0 {
			return false
		}
	}
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end < 0 {
			return false
		}
		host = host[end+1:]
		if host == "" {
			return true
		}
		if host[0] != ':' {
			return false
		}
		return validPort(host[1:])
	}
	if strings.ContainsAny(host, "[]") {
		return false
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		return validPort(host[i+1:])
	}
	return true
}

func validPort(p string) bool {
	if p == "" || len(p) > 5 {
		return false
	}
	for i := range len(p) {
		if p[i] < '0' || p[i] > '9' {
			return false
		}
	}
	return true
}

func (h *Host) requestSetAuthority(_ *hostResources, c *interp.CanonCaller, r *httpRequest, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("request.set-authority", rest, 3, "disc, ptr, len"); err != nil {
		return nil, err
	}
	a, err := liftOptString(c, "request.set-authority", rest)
	if err != nil {
		return nil, err
	}
	if a != nil && !validAuthority(*a) {
		return resultFlat(false), nil
	}
	r.locked(func() { r.authority = a })
	return resultFlat(true), nil
}

// requestGetOptions is `get-options: func() -> option<request-options>`: a fresh immutable copy each call, so dropping
// the request before it cannot trap, as it does not on wasmtime (p3http's options-child-drop row).
func (h *Host) requestGetOptions(res *hostResources, c *interp.CanonCaller, r *httpRequest, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("request.get-options", rest, 1, "ret"); err != nil {
		return nil, err
	}
	var o *httpOptions
	r.locked(func() { o = r.options })
	own := canon.OwnType(httpOptionsResource.rt)
	if o == nil {
		return store(c, rest[0], optionValue(own, nil), nil)
	}
	v := canon.Own(httpOptionsResource.rt, uint32(res.mint(httpOptionsResource, o.copyOf(true))))
	return store(c, rest[0], optionValue(own, &v), nil)
}

// requestGetHeaders is `get-headers: func() -> headers`: a fresh immutable copy each call.
func (h *Host) requestGetHeaders(res *hostResources, _ *interp.CanonCaller, r *httpRequest, _ []interp.Value) ([]interp.Value, error) {
	var f *httpFields
	r.locked(func() { f = r.headers })
	return []interp.Value{interp.I32(res.mint(httpFieldsResource, f.copyOf(true)))}, nil
}

// ---- response

// httpResponse is the object behind a `response` handle: a guest's, from response.new, with its guest body and its
// transmission future, or a send's, with the host's body (hosthttpsend.go). body, sent and host are guarded by
// asyncHandles.mu. caller is response.new's, bound to the guest's memory, so a host serving the response can read its
// body stream from that memory (hostserve.go, ADR 0137); it is never written after the mint.
type httpResponse struct {
	mu      sync.Mutex
	status  uint16
	headers *httpFields
	h       *asyncHandles
	reg     *hostHTTP
	body    httpGuestBody
	sent    *readableFutureEnd
	host    *httpSend
	caller  *interp.CanonCaller
}

func (h *Host) responseGetStatus(_ *hostResources, _ *interp.CanonCaller, r *httpResponse, _ []interp.Value) ([]interp.Value, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []interp.Value{interp.I32(int32(r.status))}, nil
}

// responseSetStatus is `set-status-code: func(status-code) -> result`: 100 to 599, as wasmtime reads it.
func (h *Host) responseSetStatus(_ *hostResources, _ *interp.CanonCaller, r *httpResponse, rest []interp.Value) ([]interp.Value, error) {
	if err := wantArgs("response.set-status-code", rest, 1, "status"); err != nil {
		return nil, err
	}
	s := uint32(rest[0].Bits) & 0xffff // def:lift_flat's u16: the word's low 16 bits
	if s < 100 || s > 599 {
		return resultFlat(false), nil
	}
	r.mu.Lock()
	r.status = uint16(s)
	r.mu.Unlock()
	return resultFlat(true), nil
}

func (h *Host) responseGetHeaders(res *hostResources, _ *interp.CanonCaller, r *httpResponse, _ []interp.Value) ([]interp.Value, error) {
	r.mu.Lock()
	f := r.headers
	r.mu.Unlock()
	return []interp.Value{interp.I32(res.mint(httpFieldsResource, f.copyOf(true)))}, nil
}

// ---- The host's tables.

// httpResourceTypes and httpMethods are the resource half's entries (resourceSource), 0.3 only.
func (h *Host) httpResourceTypes(types map[string]*hostResource) {
	serve(types, wasiP3, "wasi:http/types::fields", httpFieldsResource)
	serve(types, wasiP3, "wasi:http/types::request", httpRequestResource)
	serve(types, wasiP3, "wasi:http/types::request-options", httpOptionsResource)
	serve(types, wasiP3, "wasi:http/types::response", httpResponseResource)
}

func (h *Host) httpMethods(m map[string]resourceMethod) {
	const t = "wasi:http/types::"
	serve(m, wasiP3, t+"[constructor]fields", h.fieldsNew)
	serve(m, wasiP3, t+"[static]fields.from-list", h.fieldsFromList)
	serve(m, wasiP3, t+"[method]fields.get", method(httpFieldsResource, h.fieldsGet))
	serve(m, wasiP3, t+"[method]fields.has", method(httpFieldsResource, h.fieldsHas))
	serve(m, wasiP3, t+"[method]fields.set", method(httpFieldsResource, h.fieldsSet))
	serve(m, wasiP3, t+"[method]fields.delete", method(httpFieldsResource, h.fieldsDelete))
	serve(m, wasiP3, t+"[method]fields.get-and-delete", method(httpFieldsResource, h.fieldsGetAndDelete))
	serve(m, wasiP3, t+"[method]fields.append", method(httpFieldsResource, h.fieldsAppend))
	serve(m, wasiP3, t+"[method]fields.copy-all", method(httpFieldsResource, h.fieldsCopyAll))
	serve(m, wasiP3, t+"[method]fields.clone", method(httpFieldsResource, h.fieldsClone))

	serve(m, wasiP3, t+"[method]request.get-method", method(httpRequestResource, h.requestGetMethod))
	serve(m, wasiP3, t+"[method]request.set-method", method(httpRequestResource, h.requestSetMethod))
	serve(m, wasiP3, t+"[method]request.get-path-with-query", method(httpRequestResource, h.requestGetPath))
	serve(m, wasiP3, t+"[method]request.set-path-with-query", method(httpRequestResource, h.requestSetPath))
	serve(m, wasiP3, t+"[method]request.get-scheme", method(httpRequestResource, h.requestGetScheme))
	serve(m, wasiP3, t+"[method]request.set-scheme", method(httpRequestResource, h.requestSetScheme))
	serve(m, wasiP3, t+"[method]request.get-authority", method(httpRequestResource, h.requestGetAuthority))
	serve(m, wasiP3, t+"[method]request.set-authority", method(httpRequestResource, h.requestSetAuthority))
	serve(m, wasiP3, t+"[method]request.get-options", method(httpRequestResource, h.requestGetOptions))
	serve(m, wasiP3, t+"[method]request.get-headers", method(httpRequestResource, h.requestGetHeaders))

	serve(m, wasiP3, t+"[constructor]request-options", h.optionsNew)
	for i, name := range []string{"connect-timeout", "first-byte-timeout", "between-bytes-timeout"} {
		serve(m, wasiP3, t+"[method]request-options.get-"+name, method(httpOptionsResource, optionsGet(i)))
		serve(m, wasiP3, t+"[method]request-options.set-"+name, method(httpOptionsResource, optionsSet(i)))
	}
	serve(m, wasiP3, t+"[method]request-options.clone", method(httpOptionsResource, h.optionsClone))

	serve(m, wasiP3, t+"[method]response.get-status-code", method(httpResponseResource, h.responseGetStatus))
	serve(m, wasiP3, t+"[method]response.set-status-code", method(httpResponseResource, h.responseSetStatus))
	serve(m, wasiP3, t+"[method]response.get-headers", method(httpResponseResource, h.responseGetHeaders))
}

// httpConsumers are the four functions that carry streams and futures, each bound with both tables.
func (h *Host) httpConsumers(m map[string]*streamConsumer) {
	const t = "wasi:http/types::"
	serve(m, wasiP3, t+"[static]request.new", &streamConsumer{bindRes: h.requestNew})
	serve(m, wasiP3, t+"[static]response.new", &streamConsumer{bindRes: h.responseNew})
	serve(m, wasiP3, t+"[static]request.consume-body", &streamConsumer{bindRes: h.requestConsumeBody})
	serve(m, wasiP3, t+"[static]response.consume-body", &streamConsumer{bindRes: h.responseConsumeBody})
}

// ---- The declared signatures.

// sigKind is what a declared signature's value type is: a type the codec carries, a stream, a future, or an option or
// tuple of those, the shapes a stream or future reaches in wasi:http's functions.
type sigKind int

const (
	sigPlain sigKind = iota
	sigStream
	sigFuture
	sigOption
	sigTuple
)

// sigType is one value type of a declared signature: t is a plain type, or a stream's element, or a future's payload;
// elems is an option's element or a tuple's.
type sigType struct {
	kind  sigKind
	t     canon.Type
	elems []sigType
}

// declaredSig is an import's whole type as the host declares it: what it lifts its arguments at and lowers its result
// at. The guest's declared signature must equal it (walker.checkDeclaredSignature), or the import is refused at
// instantiate.
type declaredSig struct {
	params []sigType
	result *sigType
}

func plain(t canon.Type) sigType { return sigType{kind: sigPlain, t: t} }
func result(t sigType) *sigType  { return &t }

// httpSignatures declares every wasi:http import's type, 0.3 only.
func httpSignatures() map[string]*declaredSig {
	fields, req, opts, resp := httpFieldsResource.rt, httpRequestResource.rt, httpOptionsResource.rt, httpResponseResource.rt
	own, borrow := canon.OwnType, canon.BorrowType
	he, oe, ec := headerErrorType(), requestOptionsErrorType(), httpErrorCodeType()
	unit := canon.ResultType(nil, nil)
	ofHeader := func(ok *canon.Type) sigType { return plain(canon.ResultType(ok, &he)) }
	trailers := sigType{kind: sigFuture, t: trailersPayloadType()}
	transmit := sigType{kind: sigFuture, t: transmitPayloadType()}
	bodyStream := sigType{kind: sigStream, t: canon.Type{Kind: canon.KindU8}}
	optStream := sigType{kind: sigOption, elems: []sigType{bodyStream}}
	tuple := func(e ...sigType) sigType { return sigType{kind: sigTuple, elems: e} }
	sig := func(r *sigType, ps ...sigType) *declaredSig { return &declaredSig{params: ps, result: r} }
	fb, rb, ob, pb := plain(borrow(fields)), plain(borrow(req)), plain(borrow(opts)), plain(borrow(resp))
	str := plain(strT())
	optOwnFields, optOwnFieldsOK := fieldsOwnT(), plain(fieldsOwnT())
	vals := valuesT()
	m := map[string]*declaredSig{}
	const t = "wasi:http/types::"
	d := func(name string, s *declaredSig) { serve(m, wasiP3, t+name, s) }
	d("[constructor]fields", sig(&optOwnFieldsOK))
	d("[static]fields.from-list", sig(result(ofHeader(&optOwnFields)), plain(entriesT())))
	d("[method]fields.get", sig(result(plain(valuesT())), fb, str))
	d("[method]fields.has", sig(result(plain(canon.Type{Kind: canon.KindBool})), fb, str))
	d("[method]fields.set", sig(result(ofHeader(nil)), fb, str, plain(valuesT())))
	d("[method]fields.delete", sig(result(ofHeader(nil)), fb, str))
	d("[method]fields.get-and-delete", sig(result(ofHeader(&vals)), fb, str))
	d("[method]fields.append", sig(result(ofHeader(nil)), fb, str, plain(bytesT())))
	d("[method]fields.copy-all", sig(result(plain(entriesT())), fb))
	d("[method]fields.clone", sig(&optOwnFieldsOK, fb))

	optOpts := sigType{kind: sigOption, elems: []sigType{plain(own(opts))}}
	d("[static]request.new", sig(result(tuple(plain(own(req)), transmit)), plain(own(fields)), optStream, trailers, optOpts))
	d("[method]request.get-method", sig(result(plain(httpMethodType())), rb))
	d("[method]request.set-method", sig(result(plain(unit)), rb, plain(httpMethodType())))
	d("[method]request.get-path-with-query", sig(result(plain(optStrT())), rb))
	d("[method]request.set-path-with-query", sig(result(plain(unit)), rb, plain(optStrT())))
	d("[method]request.get-scheme", sig(result(plain(canon.OptionType(httpSchemeType()))), rb))
	d("[method]request.set-scheme", sig(result(plain(unit)), rb, plain(canon.OptionType(httpSchemeType()))))
	d("[method]request.get-authority", sig(result(plain(optStrT())), rb))
	d("[method]request.set-authority", sig(result(plain(unit)), rb, plain(optStrT())))
	d("[method]request.get-options", sig(result(plain(canon.OptionType(own(opts)))), rb))
	d("[method]request.get-headers", sig(result(plain(own(fields))), rb))
	d("[static]request.consume-body", sig(result(tuple(bodyStream, trailers)), plain(own(req)), transmit))

	optDur := plain(canon.OptionType(u64T()))
	d("[constructor]request-options", sig(result(plain(own(opts)))))
	for _, name := range []string{"connect-timeout", "first-byte-timeout", "between-bytes-timeout"} {
		d("[method]request-options.get-"+name, sig(&optDur, ob))
		d("[method]request-options.set-"+name, sig(result(plain(canon.ResultType(nil, &oe))), ob, optDur))
	}
	d("[method]request-options.clone", sig(result(plain(own(opts))), ob))

	d("[static]response.new", sig(result(tuple(plain(own(resp)), transmit)), plain(own(fields)), optStream, trailers))
	d("[method]response.get-status-code", sig(result(plain(u16T())), pb))
	d("[method]response.set-status-code", sig(result(plain(unit)), pb, plain(u16T())))
	d("[method]response.get-headers", sig(result(plain(own(fields))), pb))
	d("[static]response.consume-body", sig(result(tuple(bodyStream, trailers)), plain(own(resp)), transmit))

	ownResp := own(resp)
	serve(m, wasiP3, "wasi:http/client::send", sig(result(plain(canon.ResultType(&ownResp, &ec))), plain(own(req))))
	return m
}

// checkDeclaredSignature checks a guest's declared signature for an import whose whole type the host declares
// (ADR 0135): each parameter and the result must be the host's, handles resolved through the host's RTs, streams and
// futures by their element and payload. A guest whose types differ would be lowered into at the host's layout.
//
// NEUTER, run: return nil, and TestADeclaredSignatureMustMatchTheHosts' guest whose fields.has returns a u32
// instantiates. TestP3HTTPMatchesWasmtime passes under it: p3http declares every type as the host does.
func (w *walker) checkDeclaredSignature(lowerName string, sig *FuncType) error {
	want, ok, _ := lookupHost(w.resourceSrc.sigs, lowerName)
	if !ok || sig == nil {
		return nil
	}
	match := len(sig.Params) == len(want.params) && (sig.Result == nil) == (want.result == nil)
	for i := 0; match && i < len(want.params); i++ {
		match = w.sigMatches(sig.Params[i].Type, want.params[i], 0)
	}
	if match && want.result != nil {
		match = w.sigMatches(*sig.Result, *want.result, 0)
	}
	if !match {
		return fmt.Errorf("%w: import %s is declared by the guest with a type other than the host's (ADR 0135)",
			ErrLinkRefused, lowerName)
	}
	return nil
}

func (w *walker) sigMatches(vt ValType, want sigType, depth int) bool {
	if depth > 32 {
		return false
	}
	eq := func(v ValType, t canon.Type) bool {
		got, err := canonTypeIn(v, w.hostRT)
		return err == nil && canon.TypeEqual(got, t)
	}
	switch want.kind {
	case sigStream:
		return vt.Kind == VStream && vt.Elem != nil && eq(*vt.Elem, want.t)
	case sigFuture:
		return vt.Kind == VFuture && vt.Elem != nil && eq(*vt.Elem, want.t)
	case sigOption:
		return vt.Kind == VOption && vt.Elem != nil && w.sigMatches(*vt.Elem, want.elems[0], depth+1)
	case sigTuple:
		if vt.Kind != VTuple || len(vt.Elems) != len(want.elems) {
			return false
		}
		for i := range want.elems {
			if !w.sigMatches(vt.Elems[i], want.elems[i], depth+1) {
				return false
			}
		}
		return true
	default:
		return eq(vt, want.t)
	}
}

// ---- Converting between fields and Go's headers.

// fieldsOfHeader is a Go header as an immutable fields: names lowercased and sorted, since Go's Header is a map and
// the wire's order is lost (a named divergence, ADR 0135).
func fieldsOfHeader(hdr map[string][]string) *httpFields {
	names := make([]string, 0, len(hdr))
	for k := range hdr {
		names = append(names, k)
	}
	sort.Strings(names)
	f := &httpFields{immutable: true}
	for _, k := range names {
		if len(hdr[k]) == 0 {
			continue
		}
		g := fieldGroup{name: strings.ToLower(k)}
		for _, v := range hdr[k] {
			g.values = append(g.values, []byte(v))
		}
		if i := f.indexLocked(g.name); i >= 0 {
			f.groups[i].values = append(f.groups[i].values, g.values...)
			continue
		}
		f.groups = append(f.groups, g)
	}
	return f
}

// durationOf is a timeout as a Go duration, saturating.
func durationOf(ns uint64) int64 {
	if ns > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(ns)
}
