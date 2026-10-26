package haven

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"
)

const maxIPResponseSize = 64 // longest textual IPv6 address is 45 chars, leave room for surrounding whitespace.

var (
	providers   = []string{"ifconfig.me/ip", "icanhazip.com", "checkip.amazonaws.com"}
	cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10") // carrier-grade NAT range, not covered by [netip.Addr.IsPrivate].
	ipClient    = &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

func GetPublicIP(ctx context.Context) (netip.Addr, error) {
	// shuffle a copy so no single provider is always hit first and the shared slice is never mutated
	order := slices.Clone(providers)
	rand.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] }) //nolint:gosec // provider order only spreads load, predictability has no security impact
	for _, provider := range order {
		if err := ctx.Err(); err != nil {
			return netip.Addr{}, err
		}
		ip, err := doRequest(ctx, provider)
		if err == nil {
			return ip, nil
		}
	}
	return netip.Addr{}, errors.New("could not get any IP from providers")
}

func doRequest(ctx context.Context, provider string) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s", provider), nil)
	if err != nil {
		return netip.Addr{}, err
	}
	resp, err := ipClient.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("unexpected status from provider '%s': %s", provider, resp.Status)
	}

	// read one byte past the limit so oversized responses are rejected instead of silently truncated
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIPResponseSize+1))
	if err != nil {
		return netip.Addr{}, err
	}
	if len(body) > maxIPResponseSize {
		return netip.Addr{}, fmt.Errorf("response from provider '%s' exceeds %d bytes", provider, maxIPResponseSize)
	}

	return parsePublicIP(string(body))
}

func parsePublicIP(raw string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return netip.Addr{}, err
	}
	if addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("address %q has a zone", addr)
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || cgnatPrefix.Contains(addr) {
		return netip.Addr{}, fmt.Errorf("address %q is not a public address", addr)
	}
	return addr, nil
}
