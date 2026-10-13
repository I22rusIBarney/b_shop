package api

import (
	"html/template"
	"net/http"
	"regexp"
	"strings"
)

// GET /open?to=happ://add/… — a tiny page that hands an app link (custom scheme) to the OS.
// Telegram Mini Apps can only open http(s) links, so the cabinet opens this page in the browser and the
// page forwards to the VPN client. If the app isn't installed nothing happens, so the page explains that.

var appScheme = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]{1,31}):`)

var blockedSchemes = map[string]bool{
	"http": true, "https": true, "javascript": true, "data": true, "file": true, "vbscript": true,
	"blob": true, "about": true, "filesystem": true, "ftp": true, "ws": true, "wss": true,
}

// appLink validates the target: any app scheme except web and script ones.
func appLink(to string) (string, bool) {
	to = strings.TrimSpace(to)
	m := appScheme.FindStringSubmatch(to)
	if m == nil || len(to) > 8192 || strings.ContainsAny(to, "\r\n") {
		return "", false
	}
	if blockedSchemes[strings.ToLower(m[1])] {
		return "", false
	}
	return to, true
}

var openTpl = template.Must(template.New("open").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Открываем приложение…</title>
<style>
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#151210;color:#f3ede6;font:16px/1.45 system-ui,-apple-system,sans-serif;padding:24px;box-sizing:border-box}
main{max-width:380px;text-align:center}
h1{font-size:20px;margin:0 0 10px}
p{color:#b9ada0;margin:0 0 18px}
a.b{display:block;padding:14px 18px;border-radius:999px;background:#e8873a;color:#1b120a;font-weight:700;text-decoration:none;margin-bottom:12px}
button{width:100%;padding:13px 18px;border-radius:999px;border:2px solid #e8873a;background:none;color:#e8873a;font:inherit;font-weight:600}
small{display:block;margin-top:14px;color:#8e8378}
</style></head><body><main>
<h1>Открываем приложение…</h1>
<p>Если приложение не открылось, значит оно ещё не установлено. Вернитесь назад, установите его на первом шаге и нажмите «Добавить подписку» снова.</p>
<a class="b" id="go" href="{{.}}">Открыть приложение</a>
<button id="c" type="button">Скопировать ссылку</button>
<small>Ссылку можно вставить в приложение вручную: «Добавить» → «Из буфера обмена».</small>
</main>
<script nonce="bshop-open">
var to=document.getElementById('go').getAttribute('href');
setTimeout(function(){location.href=to},150);
document.getElementById('c').onclick=function(){
  var b=this;var ok=function(){b.textContent='Скопировано'};
  if(navigator.clipboard){navigator.clipboard.writeText(to).then(ok,ok)}else{ok()}
};
</script></body></html>`))

func (s *Server) openApp(w http.ResponseWriter, r *http.Request) {
	to, ok := appLink(r.URL.Query().Get("to"))
	if !ok {
		http.Error(w, "bad link", http.StatusBadRequest)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-bshop-open'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	// html/template refuses unknown schemes in href ("#ZgotmplZ"), the link is already validated.
	_ = openTpl.Execute(w, template.URL(to))
}
