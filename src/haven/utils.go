package haven

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

func GetPublicIP(ctx context.Context, logger zerolog.Logger) (net.Addr, error) {
	for _, provider := range providers {
		ip, err := doRequest(ctx, provider)
		if err == nil {
			return ip, nil
		}
		logger.Warn().Err(err).Msgf("failed to get IP from provider '%s': %v", provider, err)
	}
	return nil, errors.New("could not get any IP from providers")
}

var providers = []string{"ifconfig.me/ip", "icanhazip.com", "checkip.amazonaws.com"}

func doRequest(ctx context.Context, provider string) (net.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s", provider), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(strings.TrimSpace(string(body)))
	if ip == nil {
		return nil, fmt.Errorf("invalid IP from provider '%s': %q", provider, body)
	}
	return &net.IPAddr{IP: ip}, nil
}
