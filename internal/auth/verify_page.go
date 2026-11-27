package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"

	"github.com/gin-gonic/gin"
)

// verifyPageScript reads the token from the URL fragment (which never reaches a server), removes the
// fragment from the address bar, and posts the token only after an explicit submit. It writes server
// text with textContent, never innerHTML.
const verifyPageScript = `(function () {
  var message = document.getElementById('message');
  var form = document.getElementById('form');
  var password = document.getElementById('password');
  var button = document.getElementById('submit');
  var token = new URLSearchParams(location.hash.slice(1)).get('token');
  history.replaceState(null, '', location.pathname);
  if (!token) {
    message.textContent = 'This link is not complete. Open the link from the newest email.';
    return;
  }
  form.hidden = false;
  form.addEventListener('submit', function (event) {
    event.preventDefault();
    var payload = { token: token };
    if (password.value !== '') {
      payload.password = password.value;
    }
    button.disabled = true;
    fetch(location.pathname, {
      method: 'POST',
      credentials: 'omit',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    }).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (body) {
        if (response.status === 200) {
          form.hidden = true;
          message.textContent = 'Your email address is verified. Sign in with your normal login.';
        } else if (response.status === 429) {
          message.textContent = 'Too many tries. Try again later.';
        } else {
          message.textContent = body.message || 'The link did not work.';
        }
      });
    }).catch(function () {
      message.textContent = 'The request failed. Check your connection and try again.';
    }).then(function () {
      button.disabled = false;
    });
  });
})();`

const verifyPageStyle = `body { font-family: system-ui, sans-serif; max-width: 28rem; margin: 3rem auto; padding: 0 1rem; }
form[hidden] { display: none; }
label, input, button { display: block; width: 100%; box-sizing: border-box; margin-top: 0.75rem; font: inherit; }
input, button { padding: 0.5rem; }`

const verifyPageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Verify your email</title>
<style>` + verifyPageStyle + `</style>
</head>
<body>
<h1>Verify your email</h1>
<p id="message" role="status"></p>
<form id="form" hidden>
<label for="password">Password. Leave it empty if you sign in only with Google.</label>
<input id="password" type="password" autocomplete="current-password">
<button id="submit" type="submit">Verify email</button>
</form>
<script>` + verifyPageScript + `</script>
</body>
</html>
`

// verifyPageCSP allows only the one inline script and the one inline style, by hash.
var verifyPageCSP = "default-src 'none'; script-src " + cspHash(verifyPageScript) + "; style-src " + cspHash(verifyPageStyle) +
	"; connect-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'"

func cspHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// verifyPage serves the page behind the link in the verification email. It reads no data and
// writes none: mail scanners and link previews that open the link change nothing.
func (accounts *Auth) verifyPage(ctx *gin.Context) {
	headers := ctx.Writer.Header()
	headers.Set("Cache-Control", "no-store")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("Content-Security-Policy", verifyPageCSP) // replaces the global policy
	if ctx.Request.Method == http.MethodHead {
		ctx.Header("Content-Type", "text/html; charset=utf-8")
		ctx.Status(http.StatusOK)
		return
	}
	ctx.Data(http.StatusOK, "text/html; charset=utf-8", []byte(verifyPageTemplate))
}
