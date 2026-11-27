# Import Browser Bookmarks into weblinks.json

You are importing a user's browser bookmarks into `weblinks.json`, the data
file of weblinks, a personal bookmarks server. Follow every section below. The
output must be a valid weblinks store that the server can load without error
and that contains no duplicates.

## Inputs

Ask the user for anything missing before starting:

1. **The bookmark export.** Any of:
   - A Netscape bookmark HTML file. Chrome, Edge, Brave, Vivaldi, Firefox and
     Safari all export this format, so it is the preferred input.
   - Chrome's or another Chromium browser's `Bookmarks` JSON file from the
     profile directory.
   - A Firefox bookmarks backup in plain `.json` form. A `.jsonlz4` file is
     compressed; ask for an HTML export instead.

   More than one export may be given, for example from two browsers. Import
   them in the order given and deduplicate across all of them.
2. **The path to `weblinks.json`.** It may not exist yet, or it may already
   hold weblinks that must be kept.

## Ground rules

- **Use a script, never retype data.** Write a small program (Python with the
  standard library is a good choice) that parses the export and writes the
  JSON. Do not transcribe URLs or titles by hand or from memory: a single
  altered character produces a broken link that nobody notices. This holds
  even for a few dozen bookmarks.
- **Back up first.** If `weblinks.json` exists, copy it to
  `weblinks--pre-import.json` before writing anything. Hand edits do not go
  through the server, so the server's own `backups/` will not capture the
  state before the import.
- **Stop the server first.** The server writes the file when a link is
  clicked or checked. If it does so between your read and your write, one of
  the two changes is lost. Ask the user to stop it, or confirm it is not
  running.
- **Never modify existing entries.** When merging into an existing store,
  every link already present keeps its `id`, fields, section and position.

## Agree on the section mapping before writing

weblinks has a single level of named sections; it has no nested folders.
Before writing the file, inspect the export and propose a mapping from
bookmark folders to sections, then wait for the user to confirm or adjust it.

Defaults to propose:

- **Browser root folders are containers, not sections.** Their subfolders
  become sections. Links sitting directly in a root folder, or outside any
  folder, go to a section named `Unsorted`. Identify root folders by
  structure first and by name second:
  - In HTML, an `<H3>` with a `PERSONAL_TOOLBAR_FOLDER` or
    `UNFILED_BOOKMARKS_FOLDER` attribute is a root. Firefox puts its
    Bookmarks Menu contents directly under the top-level `<DL>`, with no
    folder of their own.
  - In JSON, the Chromium `roots.*` entries and the Firefox containers that
    carry a `root` property are roots.
  - By name, for top-level folders only: "Bookmarks bar", "Bookmarks Toolbar", "Bookmarks Menu", "Other
    bookmarks", "Mobile bookmarks", "Favorites", "Favourites", "Favorites
    bar" and "Other favorites", in any letter case.
- **A top-level folder becomes a section**, and the links in all of its
  subfolders are placed in it. If a top-level folder holds subfolders with
  enough links to stand on their own, propose `Parent / Child` sections for
  them instead and let the user decide.
- **Folders that would match an existing section** case-insensitively map to
  that section, keeping the existing spelling.
- **Safari's Reading List** usually holds read-later items rather than
  bookmarks. Ask whether to import it, and if so, into which section.
- **Very small sections** (one or two links) are worth pointing out as
  candidates to merge into a larger section.

Show the proposal as a table of source folder, target section and link count.
Note any folder that the defaults handle in a surprising way.

If the user wants links classified by purpose rather than by folder, and
`classification.md` is available (it sits beside `weblinks.json` in the
weblinks data directory), apply its decision rules to
each link instead of using the folder mapping. Replace its `{{sections}}`
placeholder with a JSON array of the agreed section names first. Report a low-confidence
choice as such rather than guessing silently.

## Parsing the export

### Netscape bookmark HTML

- The file's `<H1>` is its title, not a folder.
- A folder is `<DT><H3 ...>Name</H3>` followed by a `<DL>` holding its
  contents. Track nesting depth so each link knows its folder path.
- A bookmark is `<DT><A HREF="..." ADD_DATE="..." ...>Title</A>`. An optional
  `<DD>` line directly after it is the bookmark's description.
- `ADD_DATE` is seconds since the Unix epoch.
- **Decode HTML entities** in URLs, titles, folder names and descriptions.
  `&amp;` in an `HREF` is a literal `&` in the URL, and `&#39;` in a title is
  an apostrophe. Undecoded entities are a common import bug.
- Separators (`<HR>`) carry no data. Ignore them.
- Use a real HTML parser (`html.parser.HTMLParser` in Python), not regular
  expressions over the whole file. The format does not close its `<DT>` and
  `<p>` tags, so track folders by `<DL>` and `</DL>`.

### Chromium `Bookmarks` JSON

- The roots are `roots.bookmark_bar`, `roots.other` and `roots.synced`. These
  are the containers named above.
- A node with `"type": "folder"` has `children`. A node with `"type": "url"`
  has `url` and `name`.
- `date_added` is a string holding **microseconds since 1601-01-01 UTC**.
  Convert with: Unix seconds = `int(date_added) / 1_000_000 - 11_644_473_600`.

### Firefox JSON backup

- Folders have `"type": "text/x-moz-place-container"` and `children`. Links
  have `"type": "text/x-moz-place"` and `uri` and `title`. Separators are
  `text/x-moz-place-separator`.
- `dateAdded` is **microseconds since the Unix epoch**.

## Which bookmarks to import

Import a bookmark only when its URL is one of:

- `http://` or `https://` with a non-empty host;
- `file://` with an absolute path, such as `file:///Users/me/notes.txt`.

Skip and count everything else, giving the reason for each:

- `javascript:` bookmarklets;
- `place:` (Firefox smart folders and saved queries);
- browser-internal pages: `chrome://`, `edge://`, `brave://`, `about:`,
  `chrome-extension://`, `moz-extension://`;
- `data:`, `blob:`, `mailto:`, `tel:` and any other scheme;
- entries with an empty or unparsable URL.

Trim surrounding whitespace from the URL. Otherwise store it **exactly as
exported**: do not lowercase it, strip parameters or remove a trailing slash.
Normalization below is for comparison only.

## Duplicates

weblinks refuses to save two links whose URLs are equal after normalization,
but it does not check a hand-written file. You must enforce this yourself.

Normalize a URL for comparison as follows:

1. Lowercase the scheme and the host.
2. Remove `:80` from an `http` URL and `:443` from an `https` URL.
3. Drop the fragment (everything from `#`).
4. Remove one trailing `/` from the path.
5. Remove these query parameters, compared case-insensitively: any name
   starting with `utm_`, and `fbclid`, `gclid`, `dclid`, `gbraid`, `wbraid`,
   `msclkid`, `yclid`, `igshid`, `mc_cid`, `mc_eid`, `ref_src`, `ref_url`,
   `_hsenc`, `_hsmi`.
6. Sort the remaining query parameters.

Then:

- A bookmark whose normalized URL already exists in `weblinks.json` is
  skipped. The existing entry wins.
- Among imported bookmarks, the first one in export order wins. If a later
  duplicate has a description and the first has none, carry the description
  over to the first.
- Count duplicates and list each one in the report with both locations.

## Building each link

Every link must carry all twelve fields:

| Field | Value |
| --- | --- |
| `id` | 12 lowercase hexadecimal characters, random, unique across the whole file, including existing links. |
| `url` | The trimmed URL as exported. |
| `title` | The bookmark title with whitespace runs collapsed to one space and trimmed. If empty, use the URL. |
| `notes` | The bookmark's description, on one line and trimmed, or `""`. Do not invent notes. |
| `created` | The bookmark's added date as RFC 3339 in UTC, such as `2024-03-07T14:22:05Z`. If missing or zero, use the time of the import. |
| `last_clicked` | `null` |
| `click_count` | `0` |
| `skip_check` | `false` |
| `last_checked` | `null` |
| `last_status` | `0` |
| `last_result` | `""` |
| `last_detail` | `""` |

Do not import visit counts or last-visited times as clicks. They describe
browser history, not use of weblinks.

Within each section, keep links in export order. weblinks orders the display
by most recent click itself.

## Building the document

The file is one JSON object:

```json
{
  "title": "Weblinks",
  "sections": [
    {
      "name": "Reference",
      "links": [
        {
          "id": "3f9a1c07b2de",
          "url": "https://example.com/docs",
          "title": "Example documentation",
          "notes": "",
          "created": "2024-03-07T14:22:05Z",
          "last_clicked": null,
          "click_count": 0,
          "skip_check": false,
          "last_checked": null,
          "last_status": 0,
          "last_result": "",
          "last_detail": ""
        }
      ]
    }
  ]
}
```

- `title`: keep the existing title. For a new file use `Weblinks`, unless the
  user asks for something else.
- Section names are trimmed, non-empty and unique when compared
  case-insensitively.
- Existing sections keep their position. Imported links are appended to the
  end of the section they map to. New sections are appended after the
  existing ones, in the order their first link appears in the export.
- No section may be empty. Drop any that end up with no links.
- Write UTF-8 with two-space indentation and a trailing newline. Write to a
  temporary file in the same directory and rename it over `weblinks.json`, so
  a failure part way through cannot leave a truncated file.

## Verify before reporting

Re-read the written file and check all of the following. If `jq` is
available, these commands each print `true` on success:

```
jq 'type == "object" and (.title | type == "string") and (.sections | type == "array")' weblinks.json
jq '[.sections[].links[].id] | length == (unique | length)' weblinks.json
jq '[.sections[].links[].id] | all(test("^[0-9a-f]{12}$"))' weblinks.json
jq '[.sections[].name | ascii_downcase] | length == (unique | length)' weblinks.json
jq 'all(.sections[]; (.links | length) > 0 and (.name | length) > 0)' weblinks.json
jq '[.sections[].links[] | keys | length == 12] | all' weblinks.json
```

Also confirm, from your script:

- No two links share a normalized URL.
- Every link that was in `weblinks.json` before the import is still present,
  unchanged.
- Existing links plus imported links equals the total now in the file.

## Report

Finish with a short report:

- bookmarks found in each export;
- links imported, per section;
- links skipped, grouped by reason (unsupported scheme, duplicate, empty or
  unparsable URL), listing the duplicates and a sample of the rest;
- the path of the pre-import copy, and how to undo:
  `cp -f weblinks--pre-import.json weblinks.json`.

Then tell the user to start the server, open the page to confirm the
sections look right, and run **Check links** to find bookmarks that have
since gone dead. Links that need a sign-in will show as `auth` rather than
dead, and can be marked to skip checking from the edit dialog.
