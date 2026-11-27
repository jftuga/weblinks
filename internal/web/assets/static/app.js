/*
 * Client for the weblinks bookmark page. It fetches the whole document once,
 * freezes a display order per section (clicked links first by recency, then
 * unclicked alphabetically) and re-renders from that snapshot on every
 * interaction. Clicking a bookmark records the click without reordering the
 * page, so entries never move while being read; the order is recomputed only
 * on reload or after a structural change.
 */

"use strict";

/* ---------------------------------------------------------------- state */

// Sentinel value for the "create a new section" option. The empty string
// is safe because the server rejects a blank section name, so it can
// never collide with a real one.
const NEW_SECTION = "";

// Sentinel value for the "Choose a section…" placeholder shown in the
// bookmarklet's add dialog. The server trims section names, so a lone space
// can never collide with a real section or with NEW_SECTION.
const CHOOSE_SECTION = " ";

// The fields a search can be restricted to, in the order their checkboxes
// appear. Each name is both the state key and the id suffix of its checkbox.
const SEARCH_FIELDS = ["title", "url", "notes", "section"];

const KEYS = {
    collapsed: "weblinks.collapsed",
    notes: "weblinks.notes",
    fields: "weblinks.fields",
    theme: "weblinks.theme",
};

const state = {
    doc: { title: "Weblinks", sections: [] },
    order: new Map(),
    query: "",
    terms: [],
    fields: { title: true, url: true, notes: true, section: true },
    collapsed: new Set(),
    notesShown: new Set(),
    visible: [],
    activeIndex: -1,
    editingId: null,
    fromBookmarklet: false,
};

const el = {};

/* ------------------------------------------------------------ storage */

/** Reads a JSON value from localStorage, returning fallback on any failure. */
function loadPref(key, fallback) {
    try {
        const raw = localStorage.getItem(key);
        return raw === null ? fallback : JSON.parse(raw);
    } catch (e) {
        return fallback;
    }
}

/** Writes a JSON value to localStorage, ignoring quota or privacy errors. */
function savePref(key, value) {
    try {
        localStorage.setItem(key, JSON.stringify(value));
    } catch (e) {
        /* preferences are a convenience; losing them is not an error */
    }
}

/* ---------------------------------------------------------------- api */

/**
 * Performs a JSON request, throwing an Error carrying the server's message.
 * Every mutation declares application/json even with no body: the server
 * refuses any other content type, because a form or text/plain post is a
 * CORS-simple request that a page on another site could have sent.
 */
async function api(method, path, body) {
    const opts = { method, headers: {} };
    if (method !== "GET") {
        opts.headers["Content-Type"] = "application/json";
    }
    if (body !== undefined) {
        opts.body = JSON.stringify(body);
    }
    const resp = await fetch(path, opts);
    if (resp.status === 204) {
        return null;
    }
    const text = await resp.text();
    const data = text ? JSON.parse(text) : null;
    if (!resp.ok) {
        throw new Error((data && data.error) || resp.statusText);
    }
    return data;
}

/* ------------------------------------------------------------ ordering */

/**
 * Returns link IDs in display order: clicked links newest first, then
 * never-clicked links alphabetically by title. Timestamps are RFC3339 UTC,
 * so a string comparison is also a chronological one.
 */
function orderedIDs(links) {
    const clicked = links.filter((l) => l.last_clicked);
    const fresh = links.filter((l) => !l.last_clicked);
    clicked.sort((a, b) => b.last_clicked.localeCompare(a.last_clicked));
    fresh.sort((a, b) => a.title.localeCompare(b.title, undefined, { sensitivity: "base" }));
    return clicked.concat(fresh).map((l) => l.id);
}

/** Rebuilds the frozen display order for every section. */
function freezeOrder() {
    state.order = new Map();
    for (const section of state.doc.sections) {
        state.order.set(section.name, orderedIDs(section.links));
    }
}

/** Returns a section's links in frozen order, appending any not yet ordered. */
function sectionLinks(section) {
    const order = state.order.get(section.name);
    if (!order) {
        return section.links.slice();
    }
    const byID = new Map(section.links.map((l) => [l.id, l]));
    const out = [];
    for (const id of order) {
        const link = byID.get(id);
        if (link) {
            out.push(link);
            byID.delete(id);
        }
    }
    return out.concat(Array.from(byID.values()));
}

/* ------------------------------------------------------------- search */

/** Splits the query into lowercase terms; all must match for a hit. */
function parseQuery(query) {
    return query.toLowerCase().split(/\s+/).filter((t) => t.length > 0);
}

/**
 * Returns the searchable text of a link for the currently enabled fields.
 * The owning section's name is part of that text when the section field is
 * on, so searching "guitar" turns up everything filed under Guitar.
 */
function haystack(link, sectionName) {
    const parts = [];
    if (state.fields.title) parts.push(link.title);
    if (state.fields.url) parts.push(link.url);
    if (state.fields.notes) parts.push(link.notes);
    if (state.fields.section) parts.push(sectionName);
    return parts.join("\n").toLowerCase();
}

/** Reports whether a link satisfies every search term. */
function matches(link, sectionName) {
    if (state.terms.length === 0) {
        return true;
    }
    const text = haystack(link, sectionName);
    return state.terms.every((term) => text.includes(term));
}

/** Escapes text for safe insertion into HTML. */
function escapeHTML(text) {
    return String(text)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;");
}

/**
 * Escapes text and wraps every occurrence of a search term in <mark>.
 * Matching happens on the raw text and escaping on the resulting slices, so
 * an entity introduced by escaping can never be matched by accident.
 */
function highlight(text, enabled) {
    if (!enabled || state.terms.length === 0) {
        return escapeHTML(text);
    }
    const lower = text.toLowerCase();
    const spans = [];
    for (const term of state.terms) {
        let at = lower.indexOf(term);
        while (at !== -1) {
            spans.push([at, at + term.length]);
            at = lower.indexOf(term, at + 1);
        }
    }
    if (spans.length === 0) {
        return escapeHTML(text);
    }
    spans.sort((a, b) => a[0] - b[0]);
    const merged = [];
    for (const span of spans) {
        const last = merged[merged.length - 1];
        if (last && span[0] <= last[1]) {
            last[1] = Math.max(last[1], span[1]);
        } else {
            merged.push(span.slice());
        }
    }
    let html = "";
    let pos = 0;
    for (const [start, end] of merged) {
        html += escapeHTML(text.slice(pos, start));
        html += "<mark>" + escapeHTML(text.slice(start, end)) + "</mark>";
        pos = end;
    }
    return html + escapeHTML(text.slice(pos));
}

/* ------------------------------------------------------------ formatting */

const relativeFormat = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

const TIME_UNITS = [
    ["year", 31536000],
    ["month", 2592000],
    ["week", 604800],
    ["day", 86400],
    ["hour", 3600],
    ["minute", 60],
];

/** Renders an ISO timestamp as a short relative phrase such as "3 hours ago". */
function relativeTime(iso) {
    const seconds = (Date.now() - new Date(iso).getTime()) / 1000;
    for (const [unit, size] of TIME_UNITS) {
        if (seconds >= size) {
            return relativeFormat.format(-Math.floor(seconds / size), unit);
        }
    }
    return "just now";
}

/** Renders an ISO timestamp in the browser's locale for tooltips. */
function absoluteTime(iso) {
    return new Date(iso).toLocaleString();
}

// Badge styling per check category. Categories absent from this map are not
// worth showing: "alive" needs no marker and "skipped" was never probed.
const HEALTH_CLASS = {
    dead: "badge-dead",
    unknown: "badge-warn",
    auth: "badge-auth",
};

/**
 * Returns the CSS class for a link's last check result, or an empty string
 * when there is nothing worth flagging.
 */
function healthClass(link) {
    if (!link.last_checked || !link.last_detail) {
        return "";
    }
    return HEALTH_CLASS[link.last_result] || "";
}

/** Reports whether a check result represents a problem the user should see. */
function isProblem(link) {
    return link.last_result === "dead" || link.last_result === "unknown";
}

/* ------------------------------------------------------------ rendering */

/** Rebuilds the section list from state, applying the current search. */
function render() {
    const showNotesFor = state.notesShown;
    const searching = state.terms.length > 0;
    const parts = [];
    let total = 0;

    for (const section of state.doc.sections) {
        const links = sectionLinks(section).filter((link) => matches(link, section.name));
        total += links.length;
        if (searching && links.length === 0) {
            continue;
        }
        const open = searching ? true : !state.collapsed.has(section.name);
        const notesOn = showNotesFor.has(section.name);
        const rows = links.map((link) => renderRow(link, notesOn)).join("");

        parts.push(
            '<details class="section"' + (open ? " open" : "") + ' data-section="' + escapeHTML(section.name) + '">' +
                "<summary>" +
                    "<span>" + highlight(section.name, state.fields.section) + "</span>" +
                    '<span class="section-count">' + links.length + "</span>" +
                    '<span class="section-actions">' +
                        '<button type="button" class="section-rename" data-act="rename" title="Rename this section">rename</button>' +
                        '<button type="button" class="notes-toggle" data-act="notes" aria-pressed="' + (notesOn ? "true" : "false") + '">notes</button>' +
                    "</span>" +
                "</summary>" +
                '<ul class="links">' + rows + "</ul>" +
            "</details>"
        );
    }

    if (parts.length === 0) {
        parts.push('<p class="empty-state">No weblinks match this search.</p>');
    }

    el.sections.innerHTML = parts.join("");
    el.resultCount.textContent = searching ? total + " match" + (total === 1 ? "" : "es") : "";

    // Rows inside a collapsed <details> still match a selector, so scope the
    // keyboard-navigable set to open sections only. Otherwise the highlight
    // lands on rows the user cannot see.
    state.visible = Array.from(el.sections.querySelectorAll("details[open] .link-row"));
    if (state.activeIndex >= state.visible.length) {
        state.activeIndex = state.visible.length - 1;
    }
    paintActive();
}

/** Renders one bookmark row. */
function renderRow(link, showNotes) {
    const tooltip = link.notes ? link.notes + "\n\n" + link.url : link.url;

    let meta = "";
    if (link.last_clicked) {
        meta = '<span class="link-meta" title="' + escapeHTML(absoluteTime(link.last_clicked)) + '">' +
            escapeHTML(relativeTime(link.last_clicked)) + "</span>";
    }

    let badge = "";
    const healthCls = healthClass(link);
    if (healthCls) {
        const tip = link.last_detail + "\nChecked " + absoluteTime(link.last_checked);
        badge = '<span class="badge ' + healthCls + '" title="' + escapeHTML(tip) + '">' +
            escapeHTML(link.last_detail) + "</span>";
    }

    let notes = "";
    if (showNotes && link.notes) {
        notes = '<div class="link-notes">' + highlight(link.notes, state.fields.notes) + "</div>";
    }

    return (
        '<li class="link-row" data-id="' + escapeHTML(link.id) + '">' +
            '<div class="link-main">' +
                '<a class="link-title" href="' + escapeHTML(link.url) + '" target="_blank" rel="noopener noreferrer" title="' + escapeHTML(tooltip) + '">' +
                    highlight(link.title, state.fields.title) +
                "</a>" +
                meta + badge + notes +
            "</div>" +
            '<div class="row-actions">' +
                '<button type="button" data-act="copy" title="Copy URL">copy</button>' +
                '<button type="button" data-act="edit" title="Edit weblink">edit</button>' +
            "</div>" +
        "</li>"
    );
}

/** Rebuilds the recently-clicked cutout from the sixteen newest clicks. */
function renderRecent() {
    const clicked = [];
    for (const section of state.doc.sections) {
        for (const link of section.links) {
            if (link.last_clicked) {
                clicked.push(link);
            }
        }
    }
    clicked.sort((a, b) => b.last_clicked.localeCompare(a.last_clicked));
    const recent = clicked.slice(0, 16);

    el.recentEmpty.hidden = recent.length > 0;
    el.recentList.innerHTML = recent
        .map(
            (link) =>
                '<li data-id="' + escapeHTML(link.id) + '">' +
                    '<a class="recent-title" href="' + escapeHTML(link.url) + '" target="_blank" rel="noopener noreferrer" title="' + escapeHTML(link.url) + '">' +
                        escapeHTML(link.title) +
                    "</a>" +
                    '<span class="recent-when" title="' + escapeHTML(absoluteTime(link.last_clicked)) + '">' +
                        escapeHTML(relativeTime(link.last_clicked)) +
                    "</span>" +
                "</li>"
        )
        .join("");
}

/** Applies the active-row class for keyboard navigation and scrolls to it. */
function paintActive() {
    state.visible.forEach((row, i) => row.classList.toggle("active", i === state.activeIndex));
    if (state.activeIndex >= 0) {
        state.visible[state.activeIndex].scrollIntoView({ block: "nearest" });
    }
}

/* --------------------------------------------------------------- lookup */

/** Finds a link by ID across all sections. */
function findLink(id) {
    for (const section of state.doc.sections) {
        for (const link of section.links) {
            if (link.id === id) {
                return link;
            }
        }
    }
    return null;
}

/**
 * Asks the server whether a URL is already bookmarked, returning the link or
 * null. The match is the server's, which ignores a trailing slash and any
 * tracking parameters, so the bookmarklet does not offer to create a second
 * copy of a page that differs only in how its URL was shared.
 */
async function lookupURL(url) {
    const resp = await fetch("/api/links/lookup?url=" + encodeURIComponent(url));
    if (resp.status === 404) {
        return null;
    }
    if (!resp.ok) {
        throw new Error(resp.statusText);
    }
    return resp.json();
}

/** Returns the name of the section containing a link. */
function sectionOf(id) {
    for (const section of state.doc.sections) {
        if (section.links.some((l) => l.id === id)) {
            return section.name;
        }
    }
    return "";
}

/* --------------------------------------------------------------- clicks */

/**
 * Records a click without reordering the page. The row's timestamp and the
 * recently-clicked panel update in place; section order stays frozen until
 * the next reload.
 */
function recordClick(id) {
    const link = findLink(id);
    if (!link) {
        return;
    }
    api("POST", "/api/links/" + encodeURIComponent(id) + "/click")
        .then((updated) => {
            link.last_clicked = updated.last_clicked;
            link.click_count = updated.click_count;
            refreshRowMeta(id, link);
            renderRecent();
        })
        .catch((err) => toast("Could not record click: " + err.message, true));
}

/**
 * Reports whether a link is a file URL. Browsers refuse to follow one from an
 * http page, so the server opens those on this machine instead.
 */
function isFileLink(link) {
    return link.url.toLowerCase().startsWith("file:");
}

/** Asks the server to open a file link, recording the click once it has. */
function openFileLink(id) {
    api("POST", "/api/links/" + encodeURIComponent(id) + "/open")
        .then(() => recordClick(id))
        .catch((err) => toast("Could not open file: " + err.message, true));
}

/**
 * Handles a click on a bookmark anchor. A file link has its navigation
 * cancelled and is opened by the server; every other link opens normally.
 */
function followLink(event, id) {
    const link = findLink(id);
    if (link && isFileLink(link)) {
        event.preventDefault();
        openFileLink(id);
        return;
    }
    recordClick(id);
}

/** Updates a single row's relative timestamp after a click. */
function refreshRowMeta(id, link) {
    const row = el.sections.querySelector('.link-row[data-id="' + CSS.escape(id) + '"]');
    if (!row) {
        return;
    }
    let meta = row.querySelector(".link-meta");
    if (!meta) {
        meta = document.createElement("span");
        meta.className = "link-meta";
        row.querySelector(".link-title").after(meta);
    }
    meta.textContent = relativeTime(link.last_clicked);
    meta.title = absoluteTime(link.last_clicked);
}

/** Opens a link in a new tab, or via the server for a file link, and records the click. */
function openLink(id) {
    const link = findLink(id);
    if (!link) {
        return;
    }
    if (isFileLink(link)) {
        openFileLink(id);
        return;
    }
    window.open(link.url, "_blank", "noopener");
    recordClick(id);
}

/* ---------------------------------------------------------------- title */

/** Prompts for a new page title and saves it. */
async function renameTitle() {
    const answer = window.prompt("Rename page", state.doc.title);
    if (answer === null) {
        return;
    }
    const title = answer.trim();
    if (title === "" || title === state.doc.title) {
        return;
    }
    try {
        await api("PATCH", "/api/title", { title: title });
    } catch (err) {
        toast("Rename failed: " + err.message, true);
        return;
    }
    await reload();
    toast("Title renamed.");
}

/* ------------------------------------------------------------- sections */

/**
 * Renames a section, carrying its collapsed and notes preferences across.
 * Those preferences are keyed by section name, so without the migration a
 * renamed section would silently adopt whatever state a section of the new
 * name last had.
 */
async function renameSection(from) {
    const answer = window.prompt("Rename section", from);
    if (answer === null) {
        return;
    }
    const to = answer.trim();
    if (to === "" || to === from) {
        return;
    }
    try {
        await api("PATCH", "/api/sections", { from: from, to: to });
    } catch (err) {
        toast("Rename failed: " + err.message, true);
        return;
    }
    movePref(state.collapsed, KEYS.collapsed, from, to);
    movePref(state.notesShown, KEYS.notes, from, to);
    await reload();
    toast("Section renamed.");
}

/** Re-keys a section name inside a stored set and persists the result. */
function movePref(set, key, from, to) {
    if (!set.has(from)) {
        return;
    }
    set.delete(from);
    set.add(to);
    savePref(key, Array.from(set));
}

/* --------------------------------------------------------------- dialog */

/**
 * Fills the section dropdown, selecting the given section when present.
 * When requireChoice is set and no section is given, a disabled placeholder
 * is selected instead of the first section, so the user must pick one.
 */
function populateSections(selected, requireChoice) {
    const names = state.doc.sections.map((s) => s.name);
    const options = names.map(
        (name) => '<option value="' + escapeHTML(name) + '">' + escapeHTML(name) + "</option>"
    );
    options.push('<option value="' + NEW_SECTION + '">New section…</option>');
    let initial = names[0] || NEW_SECTION;
    if (names.includes(selected)) {
        initial = selected;
    } else if (requireChoice && names.length > 0) {
        options.unshift('<option value="' + CHOOSE_SECTION + '" disabled>Choose a section…</option>');
        initial = CHOOSE_SECTION;
    }
    el.section.innerHTML = options.join("");
    el.section.value = initial;
    syncNewSection();
}

/** Shows the new-section text box only when the sentinel option is chosen. */
function syncNewSection() {
    const creating = el.section.value === NEW_SECTION;
    el.newSection.hidden = !creating;
    if (!creating) {
        el.newSection.value = "";
    }
}

/** Opens the dialog for a new bookmark, optionally pre-filled. */
function openAddDialog(prefill) {
    state.editingId = null;
    el.dialogTitle.textContent = "Add weblink";
    el.deleteBtn.hidden = true;
    el.url.value = (prefill && prefill.url) || "";
    el.title.value = (prefill && prefill.title) || "";
    el.notes.value = "";
    el.skip.checked = false;
    populateSections(null, state.fromBookmarklet);
    showDialog();
}

/** Opens the dialog to edit an existing bookmark. */
function openEditDialog(id) {
    const link = findLink(id);
    if (!link) {
        return;
    }
    state.editingId = id;
    el.dialogTitle.textContent = "Edit weblink";
    el.deleteBtn.hidden = false;
    el.url.value = link.url;
    el.title.value = link.title;
    el.notes.value = link.notes;
    el.skip.checked = Boolean(link.skip_check);
    populateSections(sectionOf(id));
    showDialog();
}

/** Displays the dialog with the error cleared and focus in a useful field. */
function showDialog() {
    el.dialogError.hidden = true;
    el.dialogError.textContent = "";
    el.dialog.showModal();
    (el.url.value ? el.notes : el.url).focus();
}

/** Saves the dialog contents as a create or an update. */
async function saveDialog() {
    if (el.section.value === CHOOSE_SECTION) {
        el.section.focus();
        return showDialogError("Choose a section.");
    }
    const section = el.section.value === NEW_SECTION ? el.newSection.value.trim() : el.section.value;
    if (!section) {
        return showDialogError("Give the new section a name.");
    }
    const payload = {
        url: el.url.value.trim(),
        title: el.title.value.trim(),
        notes: el.notes.value.trim(),
        section: section,
        skip_check: el.skip.checked,
    };
    if (!payload.url) {
        return showDialogError("URL is required.");
    }

    try {
        if (state.editingId) {
            await api("PUT", "/api/links/" + encodeURIComponent(state.editingId), payload);
        } else {
            await api("POST", "/api/links", payload);
        }
    } catch (err) {
        return showDialogError(err.message);
    }

    el.dialog.close();
    if (state.fromBookmarklet && window.opener) {
        window.close();
        return;
    }
    await reload();
    toast(state.editingId ? "Weblink updated." : "Weblink added.");
}

/** Deletes the bookmark currently open in the dialog. */
async function deleteCurrent() {
    if (!state.editingId) {
        return;
    }
    const link = findLink(state.editingId);
    if (!window.confirm('Delete "' + (link ? link.title : state.editingId) + '"?')) {
        return;
    }
    try {
        await api("DELETE", "/api/links/" + encodeURIComponent(state.editingId));
    } catch (err) {
        return showDialogError(err.message);
    }
    el.dialog.close();
    await reload();
    toast("Weblink deleted.");
}

/** Shows an inline error inside the dialog. */
function showDialogError(message) {
    el.dialogError.textContent = message;
    el.dialogError.hidden = false;
}

/* ------------------------------------------------- classification help */

// The raw Markdown for the current opening. Fetch it again on every open so
// changes to the external prompt and the store's sections appear immediately.
let classifySource = null;
let classifyRequest = 0;

// Snapshot of the add/edit form taken while the help is open, so it can be
// restored afterwards.
let suspendedForm = null;

/**
 * Captures everything the user has typed into the add/edit dialog.
 * Chrome routes a single Escape press to every stacked modal and the
 * resulting cancel event is not cancelable, so the help cannot simply be
 * layered on top: the dialog underneath is closed out from under it. Instead
 * the form is suspended and restored, leaving exactly one modal open at a
 * time.
 */
function suspendForm() {
    return {
        editingId: state.editingId,
        heading: el.dialogTitle.textContent,
        deleteHidden: el.deleteBtn.hidden,
        url: el.url.value,
        title: el.title.value,
        notes: el.notes.value,
        section: el.section.value,
        newSection: el.newSection.value,
        skip: el.skip.checked,
        error: el.dialogError.hidden ? "" : el.dialogError.textContent,
    };
}

/** Reopens the add/edit dialog with previously captured values. */
function resumeForm(saved) {
    state.editingId = saved.editingId;
    el.dialogTitle.textContent = saved.heading;
    el.deleteBtn.hidden = saved.deleteHidden;
    el.url.value = saved.url;
    el.title.value = saved.title;
    el.notes.value = saved.notes;
    el.section.value = saved.section;
    syncNewSection();
    el.newSection.value = saved.newSection;
    el.skip.checked = saved.skip;

    el.dialogError.hidden = true;
    el.dialogError.textContent = "";
    el.dialog.showModal();
    if (saved.error) {
        showDialogError(saved.error);
    }
    el.section.focus();
}

/** Opens the classification help with a fresh prompt and current sections. */
async function openClassifyDialog() {
    if (el.dialog.open) {
        suspendedForm = suspendForm();
        el.dialog.close();
    }
    el.classifyDialog.showModal();
    const request = ++classifyRequest;
    classifySource = null;
    el.classifyCopy.disabled = true;
    el.classifyBody.textContent = "Loading…";
    try {
        const resp = await fetch("/api/classification", { cache: "no-store" });
        if (!resp.ok) {
            const error = await resp.json().catch(() => ({}));
            throw new Error(error.error || resp.statusText);
        }
        const source = await resp.text();
        // A response from an earlier opening must not replace a newer one.
        if (request !== classifyRequest) return;
        classifySource = source;
        el.classifyBody.innerHTML = stylizeMarkdown(classifySource);
        el.classifyCopy.disabled = false;
    } catch (err) {
        if (request !== classifyRequest) return;
        el.classifyBody.textContent = "Could not load the prompt: " + err.message;
    }
}

/**
 * Applies light structural emphasis to the markdown: headings, horizontal
 * rules, bold runs and fence markers. This is deliberately not a markdown
 * renderer; the text stays readable as the plain prompt it is.
 */
function stylizeMarkdown(text) {
    return text
        .split("\n")
        .map((line) => {
            if (line === "---") {
                return '<span class="md-rule"></span>';
            }
            if (line.startsWith("### ")) {
                return '<span class="md-h3">' + escapeHTML(line.slice(4)) + "</span>";
            }
            if (line.startsWith("## ")) {
                return '<span class="md-h2">' + escapeHTML(line.slice(3)) + "</span>";
            }
            if (line.startsWith("# ")) {
                return '<span class="md-h1">' + escapeHTML(line.slice(2)) + "</span>";
            }
            if (line.startsWith("```")) {
                return '<span class="md-fence">' + escapeHTML(line) + "</span>";
            }
            // Bold runs, applied to the already-escaped line so that markup
            // in the source cannot leak through.
            return escapeHTML(line).replace(/\*\*([^*]+)\*\*/g, '<span class="md-bold">$1</span>');
        })
        .join("\n");
}

/** Copies the raw prompt to the clipboard. */
function copyClassifyPrompt() {
    if (classifySource === null) {
        toast("The prompt has not loaded yet.", true);
        return;
    }
    copyText(classifySource, "Classification prompt copied.");
}

/* ---------------------------------------------------------- health check */

/**
 * Probes every bookmark and applies the results. An anonymous probe cannot
 * verify a page behind a sign-in, so the summary reports the categories
 * separately rather than calling everything that is not a 200 a failure.
 */
async function checkLinks() {
    el.checkBtn.disabled = true;
    const label = el.checkBtn.textContent;
    el.checkBtn.textContent = "Checking…";
    try {
        const results = await api("POST", "/api/check");
        const now = new Date().toISOString();
        const tally = { alive: 0, auth: 0, dead: 0, unknown: 0, skipped: 0 };

        for (const section of state.doc.sections) {
            for (const link of section.links) {
                const result = results[link.id];
                if (!result) {
                    continue;
                }
                link.last_status = result.status;
                link.last_result = result.category;
                link.last_detail = result.detail;
                link.last_checked = now;
                if (result.category in tally) {
                    tally[result.category] += 1;
                }
            }
        }

        render();
        const summary = [
            tally.dead + " dead",
            tally.unknown + " uncertain",
            tally.auth + " need sign-in",
            tally.alive + " ok",
        ];
        if (tally.skipped > 0) {
            summary.push(tally.skipped + " skipped");
        }
        toast(summary.join("  ·  "), tally.dead > 0);
    } catch (err) {
        toast("Check failed: " + err.message, true);
    } finally {
        el.checkBtn.disabled = false;
        el.checkBtn.textContent = label;
    }
}

/* --------------------------------------------------------------- backups */

/** Opens the backup list, fetching it each time so it is never stale. */
async function openBackupsDialog() {
    el.backupDialog.showModal();
    el.backupList.innerHTML = '<li class="muted">Loading…</li>';
    try {
        renderBackups(await api("GET", "/api/backups"));
    } catch (err) {
        el.backupList.innerHTML = '<li class="muted">Could not load backups: ' + escapeHTML(err.message) + "</li>";
    }
}

/** Renders the backup list, newest first. */
function renderBackups(backups) {
    if (!backups || backups.length === 0) {
        el.backupList.innerHTML = '<li class="muted">No backups yet.</li>';
        return;
    }
    el.backupList.innerHTML = backups
        .map((backup) => {
            const when = absoluteTime(backup.taken);
            return (
                '<li class="backup-row" data-name="' + escapeHTML(backup.name) + '" data-when="' + escapeHTML(when) + '">' +
                    '<div class="backup-main">' +
                        '<span class="backup-when">' + escapeHTML(when) + "</span>" +
                        '<span class="backup-size">' + escapeHTML(relativeTime(backup.taken)) +
                            "  ·  " + backup.links + " links in " + backup.sections + " sections</span>" +
                    "</div>" +
                    '<button type="button" class="backup-restore">restore</button>' +
                "</li>"
            );
        })
        .join("");
}

/** Replaces every bookmark with the contents of one backup. */
async function restoreBackup(name, when) {
    if (!window.confirm("Replace every weblink with the backup from " + when + "?")) {
        return;
    }
    try {
        await api("POST", "/api/backups/" + encodeURIComponent(name) + "/restore");
    } catch (err) {
        toast("Restore failed: " + err.message, true);
        return;
    }
    el.backupDialog.close();
    await reload();
    toast("Restored the backup from " + when + ".");
}

/* ---------------------------------------------------------------- theme */

const THEME_CYCLE = ["auto", "light", "dark"];

/** Applies a theme preference and reflects it in the toggle's label. */
function applyTheme(pref) {
    if (pref === "auto") {
        delete document.documentElement.dataset.theme;
    } else {
        document.documentElement.dataset.theme = pref;
    }
    el.themeBtn.textContent = "Theme: " + pref;
}

/** Advances to the next theme preference and stores it. */
function cycleTheme() {
    const current = loadPref(KEYS.theme, "auto");
    const next = THEME_CYCLE[(THEME_CYCLE.indexOf(current) + 1) % THEME_CYCLE.length];
    savePref(KEYS.theme, next);
    applyTheme(next);
}

/* ---------------------------------------------------------------- toast */

let toastTimer = null;

/** Shows a transient message in the corner of the page. */
function toast(message, isError) {
    el.toast.textContent = message;
    el.toast.classList.toggle("error", Boolean(isError));
    el.toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
        el.toast.hidden = true;
    }, 4000);
}

/* ------------------------------------------------------------- clipboard */

/** Copies text to the clipboard, falling back to a hidden textarea. */
async function copyText(text, message) {
    try {
        await navigator.clipboard.writeText(text);
    } catch (e) {
        const area = document.createElement("textarea");
        area.value = text;
        area.style.position = "fixed";
        area.style.opacity = "0";
        document.body.appendChild(area);
        area.select();
        document.execCommand("copy");
        area.remove();
    }
    toast(message || "URL copied.");
}

/* ------------------------------------------------------------- listeners */

/** Applies the current contents of the search box to the view. */
function applySearch() {
    state.query = el.search.value;
    state.terms = parseQuery(state.query);
    state.activeIndex = state.terms.length > 0 ? 0 : -1;
    el.clearBtn.disabled = state.query === "";
    render();
}

/** Empties the search box and restores the unfiltered view. */
function clearSearch() {
    el.search.value = "";
    applySearch();
}

/** Wires every event listener the page needs. */
function attachListeners() {
    el.search.addEventListener("input", applySearch);
    el.clearBtn.addEventListener("click", () => {
        clearSearch();
        el.search.focus();
    });

    el.search.addEventListener("keydown", (event) => {
        if (event.key === "ArrowDown") {
            event.preventDefault();
            moveActive(1);
        } else if (event.key === "ArrowUp") {
            event.preventDefault();
            moveActive(-1);
        } else if (event.key === "Enter" && state.activeIndex >= 0) {
            event.preventDefault();
            openLink(state.visible[state.activeIndex].dataset.id);
        }
    });

    for (const field of SEARCH_FIELDS) {
        el.fieldToggles[field].addEventListener("change", () => {
            state.fields[field] = el.fieldToggles[field].checked;
            savePref(KEYS.fields, state.fields);
            render();
        });
    }

    el.sections.addEventListener("click", onSectionsClick);
    el.recentList.addEventListener("click", (event) => {
        const anchor = event.target.closest("a");
        if (anchor) {
            followLink(event, anchor.closest("li").dataset.id);
        }
    });

    el.classifyBtn.addEventListener("click", openClassifyDialog);
    el.classifyCopy.addEventListener("click", copyClassifyPrompt);
    el.classifyClose.addEventListener("click", () => el.classifyDialog.close());

    // However the help is dismissed - button, Escape or backdrop - the form
    // it interrupted comes back with its values intact.
    el.classifyDialog.addEventListener("close", () => {
        if (suspendedForm) {
            const saved = suspendedForm;
            suspendedForm = null;
            resumeForm(saved);
        }
    });

    el.backupsBtn.addEventListener("click", openBackupsDialog);
    el.backupClose.addEventListener("click", () => el.backupDialog.close());
    el.backupList.addEventListener("click", (event) => {
        const button = event.target.closest(".backup-restore");
        if (button) {
            const row = button.closest(".backup-row");
            restoreBackup(row.dataset.name, row.dataset.when);
        }
    });

    el.titleRename.addEventListener("click", renameTitle);
    el.addBtn.addEventListener("click", () => openAddDialog(null));
    el.checkBtn.addEventListener("click", checkLinks);
    el.themeBtn.addEventListener("click", cycleTheme);
    el.section.addEventListener("change", syncNewSection);
    el.cancelBtn.addEventListener("click", () => el.dialog.close());
    el.deleteBtn.addEventListener("click", deleteCurrent);
    el.form.addEventListener("submit", (event) => {
        event.preventDefault();
        saveDialog();
    });

    document.addEventListener("keydown", onGlobalKey);
}

/** Handles clicks inside the section list: links, row actions and headers. */
function onSectionsClick(event) {
    const action = event.target.closest("button[data-act]");
    if (action) {
        event.preventDefault();
        event.stopPropagation();
        handleAction(action);
        return;
    }

    const anchor = event.target.closest("a.link-title");
    if (anchor) {
        followLink(event, anchor.closest(".link-row").dataset.id);
        return;
    }

    const summary = event.target.closest("summary");
    if (summary) {
        // Collapse state is owned by this script rather than the <details>
        // element so that a search can force sections open without
        // overwriting what the user chose.
        event.preventDefault();
        const name = summary.parentElement.dataset.section;
        if (state.collapsed.has(name)) {
            state.collapsed.delete(name);
        } else {
            state.collapsed.add(name);
        }
        savePref(KEYS.collapsed, Array.from(state.collapsed));
        render();
    }
}

/** Dispatches a row or section-header button. */
function handleAction(button) {
    const act = button.dataset.act;
    if (act === "rename") {
        renameSection(button.closest(".section").dataset.section);
        return;
    }
    if (act === "notes") {
        const name = button.closest(".section").dataset.section;
        if (state.notesShown.has(name)) {
            state.notesShown.delete(name);
        } else {
            state.notesShown.add(name);
        }
        savePref(KEYS.notes, Array.from(state.notesShown));
        render();
        return;
    }

    const id = button.closest(".link-row").dataset.id;
    if (act === "copy") {
        const link = findLink(id);
        if (link) {
            copyText(link.url);
        }
    } else if (act === "edit") {
        openEditDialog(id);
    }
}

/** Moves the keyboard highlight by delta rows. */
function moveActive(delta) {
    if (state.visible.length === 0) {
        return;
    }
    const next = state.activeIndex + delta;
    state.activeIndex = Math.max(0, Math.min(state.visible.length - 1, next));
    paintActive();
}

/** Handles the page-level shortcuts: / to search and Escape to clear. */
function onGlobalKey(event) {
    // Any open dialog owns the keyboard; Escape is handled natively there.
    if (document.querySelector("dialog[open]")) {
        return;
    }
    const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(event.target.tagName);

    if (event.key === "/" && !typing) {
        event.preventDefault();
        el.search.focus();
        el.search.select();
        return;
    }
    if (event.key === "Escape") {
        clearSearch();
        el.search.blur();
        return;
    }
    if (!typing && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
        event.preventDefault();
        moveActive(event.key === "ArrowDown" ? 1 : -1);
    }
}

/* ----------------------------------------------------------------- init */

/** Fetches the document, refreezes the order and repaints everything. */
async function reload() {
    state.doc = await api("GET", "/api/bookmarks");
    state.doc.sections = state.doc.sections || [];
    document.title = state.doc.title;
    el.pageTitle.textContent = state.doc.title;
    freezeOrder();
    render();
    renderRecent();
}

/** Caches element references, restores preferences and loads the data. */
async function init() {
    el.pageTitle = document.getElementById("page-title");
    el.titleRename = document.getElementById("title-rename");
    el.search = document.getElementById("search");
    el.clearBtn = document.getElementById("clear-search");
    el.resultCount = document.getElementById("result-count");
    el.sections = document.getElementById("sections");
    el.recentList = document.getElementById("recent-list");
    el.recentEmpty = document.getElementById("recent-empty");
    el.dialog = document.getElementById("link-dialog");
    el.form = document.getElementById("link-form");
    el.dialogTitle = document.getElementById("dialog-title");
    el.dialogError = document.getElementById("dialog-error");
    el.url = document.getElementById("f-url");
    el.title = document.getElementById("f-title");
    el.notes = document.getElementById("f-notes");
    el.section = document.getElementById("f-section");
    el.newSection = document.getElementById("f-new-section");
    el.skip = document.getElementById("f-skip");
    el.classifyBtn = document.getElementById("classify-btn");
    el.classifyDialog = document.getElementById("classify-dialog");
    el.classifyBody = document.getElementById("classify-body");
    el.classifyCopy = document.getElementById("classify-copy");
    el.classifyClose = document.getElementById("classify-close");
    el.deleteBtn = document.getElementById("delete-btn");
    el.cancelBtn = document.getElementById("cancel-btn");
    el.addBtn = document.getElementById("add-btn");
    el.checkBtn = document.getElementById("check-btn");
    el.backupsBtn = document.getElementById("backups-btn");
    el.backupDialog = document.getElementById("backup-dialog");
    el.backupList = document.getElementById("backup-list");
    el.backupClose = document.getElementById("backup-close");
    el.themeBtn = document.getElementById("theme-btn");
    el.toast = document.getElementById("toast");
    el.fieldToggles = {};
    for (const field of SEARCH_FIELDS) {
        el.fieldToggles[field] = document.getElementById("field-" + field);
    }

    applyTheme(loadPref(KEYS.theme, "auto"));

    // Assigning over the defaults leaves a field the stored preference predates
    // at its default, so section search is on for someone upgrading.
    state.fields = Object.assign(state.fields, loadPref(KEYS.fields, {}));
    for (const field of SEARCH_FIELDS) {
        el.fieldToggles[field].checked = state.fields[field];
    }
    state.collapsed = new Set(loadPref(KEYS.collapsed, []));
    state.notesShown = new Set(loadPref(KEYS.notes, []));

    attachListeners();

    try {
        await reload();
    } catch (err) {
        el.sections.innerHTML = '<p class="empty-state">Could not load weblinks: ' + escapeHTML(err.message) + "</p>";
        return;
    }

    const prefillURL = document.body.dataset.prefillUrl || "";
    if (!prefillURL) {
        el.search.focus();
        return;
    }

    state.fromBookmarklet = true;
    let existing = null;
    try {
        existing = await lookupURL(prefillURL);
    } catch (err) {
        // A failed lookup only costs the duplicate check; adding still works.
        existing = null;
    }
    if (existing) {
        openEditDialog(existing.id);
        showDialogError("Already saved as a weblink. Editing the existing entry.");
    } else {
        openAddDialog({ url: prefillURL, title: document.body.dataset.prefillTitle || "" });
    }
}

init();
