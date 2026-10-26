package handler

import "testing"

func TestCompanyIdentity(t *testing.T) {
	for _, domain := range []string{"baidu.com", "sz.baidu.com", "hk.baidu.com"} {
		if !allowedCompanyDomain([]string{"baidu.com"}, domain) {
			t.Fatal(domain)
		}
	}
	for _, domain := range []string{"a.b.baidu.com", "fakebaidu.com", "sz.baidu.com.evil", "-sz.baidu.com", ".baidu.com"} {
		if allowedCompanyDomain([]string{"baidu.com"}, domain) {
			t.Fatal(domain)
		}
	}
	for user, email := range map[string]string{"alice": "alice@baidu.com", "employee_sz": "employee@sz.baidu.com", "alice_hk": "alice@hk.baidu.com"} {
		got, ok := machineIdentityEmail(user, "baidu.com")
		if !ok || got != email {
			t.Fatalf("%s: %s", user, got)
		}
	}
	for _, user := range []string{"a_b_c", "a_", "_a", "a_-sz"} {
		if _, ok := machineIdentityEmail(user, "baidu.com"); ok {
			t.Fatal(user)
		}
	}
}
