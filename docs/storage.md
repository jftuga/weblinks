# Storage

Weblinks are kept in a single JSON file, `weblinks.json` in the
[data directory](install.md#the-data-directory) by default (`-file` to change
it):

```json
{
  "title": "My Weblinks",
  "sections": [
    {
      "name": "Main",
      "links": [
        {
          "id": "a1b2c3d4e5f6",
          "url": "https://example.com",
          "title": "Example",
          "notes": "",
          "created": "2026-09-14T00:00:00Z",
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

`last_result` is the check category (`alive`, `auth`, `dead`, `unknown` or
`skipped`) and `last_detail` is the short phrase shown on the badge.
`last_status` is the HTTP status code, or `0` when no response arrived. A
`null` `last_checked` means the link has never been probed. `skip_check`
excludes the link from checks; turning it on in the edit dialog also clears
any stored result. See [Link checking](usage.md#link-checking) for what each
category means.

Writes go to a temporary file in the same directory and are renamed into
place, so an interrupted write cannot truncate the store. The file can be
edited by hand while the server is running: it is re-read on the next request
after its modification time or size changes. Two caveats:

- A click or a link check that the server writes while your editor has the
  file open is lost when you save over it. For a large edit, such as an
  import, stop the server first.
- If a hand edit leaves the file invalid, every request that reads or changes
  the weblinks fails with the parse error and the server writes nothing until you fix the file or restore a
  backup.

## Backups

Structural changes (adding, editing, moving or deleting a link, renaming a
section, changing the page title, and restoring a backup) copy the previous
contents into
`backups/` alongside the store, keeping the most recent `backupsKept`
(`internal/bookmarks/backup.go`, currently 30) and skipping a
backup identical to the newest. Clicks and dead-link results are written
without a backup, so routine use cannot flush the history.

The **Backups** button lists them newest first, each with when it was taken
and how many links in how many sections it holds, so you can pick by contents
rather than by timestamp. Restoring replaces every weblink with that version.
The document being replaced is backed up on the way past, so restoring the
wrong one is itself undoable; deleting a weblink by mistake is recovered by
restoring the newest backup.

## Duplicate detection

A URL is compared on its normalized form, not character by character: the
scheme and host are lowercased, a redundant `:80` or `:443` and the fragment
are dropped, a trailing slash is removed, and tracking parameters (`utm_*`,
`fbclid`, `gclid` and similar) are stripped before the rest of the query is
sorted. Only the comparison is normalized; the URL is stored exactly as you
entered it.

Without this, the same page copied from the address bar and pasted from a
Slack share link are two different weblinks, which is precisely the case the
[bookmarklet's](usage.md#bookmarklet) "edit the existing entry instead"
behavior exists for.
