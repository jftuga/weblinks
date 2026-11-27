# Request guard

The server has no authentication, so a loopback address alone is not a
boundary: every page you visit can reach it. A guard in front of every route
enforces the same-origin policy that would otherwise only be assumed.

| Check | Applies to | Refused with |
| --- | --- | --- |
| `Host` names this server | every route | 403 |
| The request is not cross-origin | `/api/*` | 403 |
| Mutations are `application/json` | `/api/*` | 415 |

The first two checks are the defenses; the third is a backstop behind the
second:

- **The `Host` check stops DNS rebinding.** A hostile site can point its own
  name at `127.0.0.1`, and from then on the browser treats it as same-origin
  and every other check passes. IP literals and `localhost` are always
  accepted, since a literal means the browser really did connect to that
  address. So is the host name given in `-addr`, because that is the name the
  server was told it answers to. Any other name has to be listed in
  `-allow-host`: a server started with `-addr 0.0.0.0:5309` and reached as
  `http://mymac.local:5309` needs `-allow-host mymac.local`.
- **The origin check stops a foreign page from driving the API.** A request
  is refused when `Sec-Fetch-Site` reports anything other than
  `same-origin` or `none` (the address was typed or opened from a bookmark).
  Independently, a request carrying an `Origin` header is refused unless that
  origin's host passes the `Host` check above; this covers browsers that do
  not send `Sec-Fetch-Site`. A request carrying neither header passes. It
  comes either from a non-browser client such as `curl`, or from an older
  browser making a plain `GET`, such as an image load. Every `GET` under
  `/api/` is read-only, and the page that triggered it cannot read the
  response, so nothing is changed or leaked.
- **The content-type check backs up the origin check.** A `text/plain` or
  form POST is a CORS-simple request: the browser sends it without asking
  first, and only the reply is withheld from the attacker. That would be
  enough to plant a weblink that looks like one of yours but points somewhere
  else. Current browsers label such a request with `Sec-Fetch-Site` and
  `Origin`, so the origin check already refuses it. Requiring
  `application/json` still stops it in an older browser that sends neither
  header on a cross-origin POST, because a JSON body forces a CORS preflight
  and the server never approves one: a cross-origin `OPTIONS` request is
  refused like any other.
