package pair

import "testing"

func TestDeriveAndProofs(t *testing.T) {
	a, err := Derive("Admin", "password123")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Derive(" admin ", "password123")
	c, _ := Derive("admin", "password124")
	if a.Group != b.Group || a.Auth != b.Auth {
		t.Fatal("одинаковые логин (без учёта регистра) и пароль должны давать одну группу")
	}
	if a.Group == c.Group {
		t.Fatal("разные пароли дали одну группу")
	}
	req := Request{Group: a.Group, Machine: "m", Host: "pc", Nonce: "00112233445566778899aabbccddeeff", TS: 1700000000, FP: "fp"}
	a.Sign(&req)
	if !b.CheckRequest(req) {
		t.Fatal("верный запрос отклонён")
	}
	if c.CheckRequest(req) {
		t.Fatal("запрос с чужим паролем принят")
	}
	req.FP = "other"
	if a.CheckRequest(req) {
		t.Fatal("подмена отпечатка не обнаружена")
	}
	resp := Response{DeviceID: "d", Token: "t", Name: "hub"}
	a.SignResponse("n1", &resp)
	if !b.CheckResponse("n1", resp) || c.CheckResponse("n1", resp) || a.CheckResponse("n2", resp) {
		t.Fatal("проверка ответа работает неверно")
	}
}
