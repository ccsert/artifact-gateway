package repository

import (
	"strings"
	"testing"
)

func TestEgressProxyValidate(t *testing.T) {
	valid := &EgressProxy{Mode: EgressProxyModeCustom, Protocol: EgressProxyProtocolSOCKS5, Host: "proxy.corp.example", Port: 1080, RemoteDNS: true, NoProxy: []string{"*.internal.example", "10.0.0.0/8"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, proxy := range map[string]*EgressProxy{
		"unknown mode":            {Mode: "socks4"},
		"custom without host":     {Mode: EgressProxyModeCustom, Protocol: EgressProxyProtocolHTTP, Port: 8080},
		"custom bad port":         {Mode: EgressProxyModeCustom, Protocol: EgressProxyProtocolHTTP, Host: "proxy.example", Port: 0},
		"custom host with scheme": {Mode: EgressProxyModeCustom, Protocol: EgressProxyProtocolHTTP, Host: "http://proxy.example", Port: 8080},
		"remoteDns on http":       {Mode: EgressProxyModeCustom, Protocol: EgressProxyProtocolHTTP, Host: "proxy.example", Port: 8080, RemoteDNS: true},
		"direct with fields":      {Mode: EgressProxyModeDirect, Host: "proxy.example"},
		"noProxy with URL":        {Mode: EgressProxyModeCustom, Protocol: EgressProxyProtocolHTTP, Host: "proxy.example", Port: 8080, NoProxy: []string{"https://internal.example"}},
	} {
		if err := proxy.Validate(); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
	for _, mode := range []EgressProxyMode{EgressProxyModeDirect, EgressProxyModeEnvironment} {
		if err := (&EgressProxy{Mode: mode}).Validate(); err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
	}
	var nilProxy *EgressProxy
	if err := nilProxy.Validate(); err != nil {
		t.Fatalf("nil proxy: %v", err)
	}
}

func TestEgressProxyValidateMessageQuality(t *testing.T) {
	err := (&EgressProxy{Mode: EgressProxyModeCustom, Protocol: "socks4", Host: "proxy.example", Port: 1080}).Validate()
	if err == nil || !strings.Contains(err.Error(), "http or socks5") {
		t.Fatalf("err = %v", err)
	}
}
