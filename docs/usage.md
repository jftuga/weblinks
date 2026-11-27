# Using the page

- **Search** filters as you type. Multiple words are ANDed, and every match is
  highlighted. The title, url, notes and section checkboxes control which
  fields are searched; the choice is remembered per browser. Section matches on
  the name of the section a link is filed under, so searching `guitar` returns
  everything in a Guitar section whether or not the word appears in the link
  itself.
- **Title** defaults to `Weblinks` for a new store. The `rename` button beside
  it changes the heading and browser tab title; a blank title is refused.
- **Sections** open and close when you click their header. Each section has
  its own `notes` toggle that reveals the notes for every link in it at once.
  Both states persist in `localStorage`.
- **Rename** a section with the `rename` button in its header. The links and
  the section's position in the page are untouched, and its collapsed and
  notes preferences follow the new name. A name another section already holds
  is refused rather than merging the two; changing only the letter case is
  allowed. Classification help uses the updated section names the next time
  you open it.
- **Order** within a section is clicked links first, most recent at the top,
  then never-clicked links alphabetically. The order is computed on load and
  frozen, so nothing moves under the cursor while you read. Reload to re-sort.
- **Recently clicked** in the top right lists the 16 most recent clicks
  across every section. Beneath it, the program version links to the
  project page.
- **Keyboard**: `/` focuses the search box, arrow keys move the highlight,
  `Enter` opens the highlighted link, `Escape` clears the search.
- **Add / edit / delete**: the **Add weblink** button and each row's `edit`
  button open the same dialog; when editing, it also has a **Delete** button.
  A row's `edit` and `copy` (copy the URL) buttons appear when the row is
  hovered or highlighted. The section dropdown's **New section…** option
  creates a section, and moving or deleting the last link in a section
  removes the empty section.
- **Classify**: the `?` beside the Section label shows a prompt you can paste
  into an LLM to decide where a new URL belongs. See
  [Choosing a section](#choosing-a-section).
- **File links**: a `file:///absolute/path` URL can be saved like any other.
  Browsers refuse to follow a file URL from an http page, so clicking one asks
  the server to open it with the operating system's handler (`open` on macOS),
  which means the server must be running on the machine that holds the file.
  Opening an application or script this way launches it.
- **Check links** probes every link that is not excluded or local, and sorts
  the outcome into categories. See
  [Link checking](#link-checking), because an anonymous probe cannot verify a
  page that needs a sign-in.
- **Backups** lists the rolling backups with what each one holds, and restores
  the one you pick. See [Backups](storage.md#backups).
- **Theme** cycles automatic, light and dark.

## Bookmarklet

Click **Bookmarklet** in the page header (it opens `/bookmarklet`) and drag
the link to your bookmarks bar. Clicking it on
any page opens a small window with the URL and title filled in. It navigates
to this server rather than posting to the API, so no site you visit can reach
the API itself. If the URL is already saved, the dialog opens on the
existing entry instead of creating a duplicate.

The server address is baked into the bookmarklet, so re-drag it after changing
`-addr`.

## Choosing a section

Sections can follow your interests, projects, or tasks. The generic prompt
classifies by why you would open a link rather than by its host: a repository
might be a tool, a learning resource, or part of a project.

The `?` next to the Section label in the add/edit dialog opens the
classification prompt, with a Copy button. Paste it into an LLM followed by
the URL and title, and it replies with one section name, a suggested title and
notes, and whether the page needs `skip_check`.

### The prompt file

The prompt is an external file. By default the server reads
`classification.md` in the [data directory](install.md#the-data-directory),
writing the generic prompt built into the binary there on first run; use
`-classification-file` to choose another path. The generic prompt is an
example you can edit or replace. It must contain the literal placeholder
`{{sections}}`, which the server replaces with a JSON array of current section
names in document order. No other template syntax is evaluated. You can add
descriptions and decision rules for your own interests without changing Go or
JavaScript code.

Every opening fetches `GET /api/classification` without caching. The server
re-reads the prompt and obtains the current sections from the store for each
request. File edits and section additions, renames, and removals therefore
appear on the next opening, without a restart, rebuild, or page refresh.
Changes made while help is open appear when you close and reopen it.

The included prompt chooses from existing sections when any exist. For a new,
empty store, it suggests Reference, Learning, News & Reading, Tools & Services,
Projects, Personal & Admin, or Other. These are suggestions only: create the
chosen section when saving a link. Once sections exist, the prompt uses those
names; it does not seed or modify the store. Custom descriptions that name
specific sections should still be kept in sync with your own renames.

### Errors

If the file is missing, unreadable, empty, or lacks `{{sections}}`, help shows
an error and disables copying. The rest of the app remains available. Fix the
file and reopen help to retry; there is no embedded fallback prompt.
The server reads and validates the prompt at startup, logging a warning if
the file is missing, unreadable, empty, or lacks `{{sections}}`. Startup remains
non-fatal. The same validation runs on every classification request, so fixes
take effect without restarting; request failures are also logged.

## Link checking

The checker runs on the server with no cookies and no credentials, so it sees
what an anonymous visitor sees. That means it cannot confirm a page behind a
sign-in still exists, and reporting every non-200 as broken would be wrong for
many personal or work collections. Results are therefore bucketed:

| Category | Meaning | Shown as |
| --- | --- | --- |
| `alive` | Responded with a 2xx status. | nothing |
| `auth` | 401, 403, 407, or a redirect to a sign-in page. The page probably exists; it cannot be verified anonymously. | muted badge |
| `dead` | Host has no DNS record, the connection was refused, or the URL is malformed. Unambiguous. | red badge |
| `unknown` | Any other status (404, 410, 429, 5xx and so on), a timeout, a TLS failure, or a DNS lookup that failed for a reason other than a missing record. Ambiguous. | amber badge |
| `skipped` | A loopback URL, or one you excluded. | nothing |

A file link is checked on disk rather than over HTTP: `alive` when the file
exists, `dead` when it does not, and `unknown` when it cannot be read.

Two deliberate choices:

- **A 404 is `unknown`, not `dead`.** Private GitHub repositories return 404 to
  unauthenticated requests precisely so they do not leak whether a repository
  exists. The same applies to many internal tools.
- **A timeout is `unknown`, not `dead`.** An intranet host that times out while
  you are off the VPN is not a broken link.

Sign-in redirects are detected by a heuristic over the post-redirect URL,
matching common identity-provider hostnames and path conventions such as
`/login`, `/sso` and `/oauth`. It will miss bespoke login flows.

For links no anonymous probe can ever validate, tick **Exclude from link
checking** in the edit dialog. Enabling it clears any stale result, so a link
you have marked unverifiable stops carrying an old badge.

Probes are `HEAD`, retried as `GET` when a server rejects the method. Loopback
URLs are skipped, since a local development server being down says nothing
about the link. Concurrency and the per-request timeout are set with
`-check-concurrency` and `-check-timeout`.
