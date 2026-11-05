// Uses the appearance state and applyAppearance from the inline head script.
// A home screen app can resume hours later: show current statuses, not the old page.
const loaded = Date.now();
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible" && Date.now() - loaded > 5 * 60 * 1000) location.reload();
});
// Choices apply immediately and leave the menu open.
const appearanceButton = document.getElementById("appearance-button");
const appearancePanel = document.getElementById("appearance");
function syncAppearance() {
  applyAppearance();
  appearancePanel.querySelector(`input[name="appearance-mode"][value="${appearanceMode}"]`).checked = true;
  appearancePanel.querySelector(`input[name="appearance-palette"][value="${appearancePalette}"]`).checked = true;
}
function positionAppearance() {
  const bounds = appearanceButton.getBoundingClientRect();
  appearancePanel.style.setProperty("--appearance-top", (bounds.bottom + 8) + "px");
  appearancePanel.style.setProperty("--appearance-right", (document.documentElement.clientWidth - bounds.right) + "px");
}
appearancePanel.addEventListener("beforetoggle", event => {
  if (event.newState === "open") positionAppearance();
});
appearancePanel.addEventListener("toggle", event => {
  appearanceButton.setAttribute("aria-expanded", event.newState === "open" ? "true" : "false");
  if (event.newState === "open") {
    appearancePanel.querySelector('input[name="appearance-mode"]:checked').focus({ preventScroll: true });
  }
});
appearancePanel.addEventListener("change", event => {
  const input = event.target;
  if (input.name === "appearance-mode") appearanceMode = input.value;
  else if (input.name === "appearance-palette") appearancePalette = input.value;
  else return;
  syncAppearance();
  // A default choice is stored as no value, so the page keeps following its default.
  const save = (key, value, fallback) => value === fallback ? localStorage.removeItem(key) : localStorage.setItem(key, value);
  try {
    // Keep the key name so earlier light/dark choices survive.
    save("tailhome-theme", appearanceMode, "system");
    save("tailhome-palette", appearancePalette, "default");
  } catch {}
});
appearancePanel.addEventListener("keydown", event => {
  if (event.key === "Escape") {
    event.preventDefault();
    appearancePanel.hidePopover();
    appearanceButton.focus();
  }
});
appearanceMedia.addEventListener("change", syncAppearance);
addEventListener("resize", positionAppearance);
syncAppearance();
const status = document.getElementById("status");
const clearManual = () => {
  document.querySelectorAll(".manual").forEach((n) => n.remove());
  document.querySelectorAll(".snippet .copied:not(.done)").forEach((m) => { m.textContent = m.dataset.idle; });
};
async function copy(value, trigger) {
  try { await navigator.clipboard.writeText(value); return true; } catch {}
  // The Clipboard API needs HTTPS; this older path also works on plain HTTP, during a click.
  const back = document.activeElement; // select() moves focus; give it back after
  const ta = Object.assign(document.createElement("textarea"), { value });
  ta.style.cssText = "position:fixed;opacity:0";
  (trigger.closest("dialog") || document.body).append(ta);
  ta.focus({ preventScroll: true });
  ta.select();
  let ok = false;
  if (document.activeElement === ta && ta.selectionStart === 0 && ta.selectionEnd === value.length) {
    try { ok = document.execCommand("copy"); } catch {}
  }
  ta.remove();
  back?.focus({ preventScroll: true });
  return ok;
}
document.addEventListener("click", async (ev) => {
  if (ev.target.closest(".manual")) return; // leave the value selectable
  clearManual();
  const el = ev.target.closest("[data-copy]");
  if (!el) return;
  const value = el.dataset.copy;
  if (await copy(value, el)) {
    // Rows show " · Copied" after their label; snippets swap their "Copy" hint.
    const mark = el.querySelector(".copied");
    mark.textContent = mark.dataset.done ?? " · Copied";
    mark.classList.add("done");
    clearTimeout(mark.timer);
    mark.timer = setTimeout(() => { mark.textContent = mark.dataset.idle ?? ""; mark.classList.remove("done"); }, 1500);
    status.textContent = "Copied " + value;
    return;
  }
  // Both paths refused: show the value selected so Ctrl+C or ⌘C copies it.
  status.textContent = "Copying is blocked. " + value + " is selected: press Command+C or Ctrl+C.";
  if (el.matches(".snippet")) { // the snippet already shows the whole value
    getSelection().selectAllChildren(el.querySelector("code"));
    el.querySelector(".copied").textContent = "Press ⌘C or Ctrl+C";
    return;
  }
  const box = document.createElement("div");
  box.className = "manual";
  const code = document.createElement("code");
  code.textContent = value;
  const hint = document.createElement("span");
  hint.textContent = "Copying is blocked here. Press ⌘C or Ctrl+C.";
  box.append(code, hint);
  el.querySelector(".text").append(box);
  getSelection().selectAllChildren(code);
});

// No sharing requests or controls exist when the feature is off.
if (document.getElementById("sharing")) {
  const byID = id => document.getElementById(id);
  const root = byID("sharing"), summary = byID("share-summary"), reminders = byID("share-reminders");
  const confirm = byID("share-confirm"), manage = byID("share-manage"), explanation = byID("share-ineligible");
  const targets = new Map();
  const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
  const transportMessage = "Status could not be updated. Public access may still be active.";
  let records = [], admin = false, selected, approvedRevision, returnFocus, operationID, busy = false, lastSnapshot, generation = 0, glyphSequence = 0;
  function shareGlobe(isPublic = false) {
    const id = "share-glyph-" + ++glyphSequence;
    const clip = `<clipPath id="${id}-clip"><circle cx="7" cy="7" r="6"/></clipPath>`;
    if (isPublic) return `<svg viewBox="0 0 14 14" aria-hidden="true"><defs>${clip}<mask id="${id}-mask" maskUnits="userSpaceOnUse" x="0" y="0" width="14" height="14"><circle cx="7" cy="7" r="7" fill="white"/><g fill="none" stroke="black" stroke-width="1" clip-path="url(#${id}-clip)"><ellipse cx="7" cy="7" rx="2.5" ry="6.5"/><path d="M0 7.5H14"/></g></mask></defs><circle cx="7" cy="7" r="7" fill="currentColor" mask="url(#${id}-mask)"/></svg>`;
    return `<svg viewBox="0 0 14 14" aria-hidden="true"><defs>${clip}</defs><g fill="none" stroke="currentColor" stroke-width="1"><circle cx="7" cy="7" r="6.5"/><g clip-path="url(#${id}-clip)"><ellipse class="share-meridian" cx="7" cy="7" rx="2.5" ry="6.5"/><path class="share-pending-meridian" display="none" d="M7 .5 C7 .5 7 13.5 7 13.5"/><path d="M0 7.5H14"/></g></g></svg>`;
  }
  function spinner() {
    const span = document.createElement("span");
    span.className = "share-spinner"; span.setAttribute("aria-hidden", "true");
    return span;
  }
  function updateActions() {
    document.querySelectorAll(".share-dialog button:not([data-share-close]), .share-reminder button").forEach(button => button.disabled = busy || !admin);
  }
  const calendarDate = value => new Date(value).toLocaleDateString("en-GB", { day: "numeric", month: "long", ...(new Date(value).getFullYear() !== new Date().getFullYear() ? { year: "numeric" } : {}) });
  function shortDate(value) {
    const date = new Date(value);
    const time = date.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
    return (date.toDateString() === new Date().toDateString() ? "" : calendarDate(value) + " ") + time;
  }
  const until = record => record.expiresAt ? "until " + shortDate(record.expiresAt) : "until an admin stops it";
  const freshPublicAddress = record => record?.public && Date.now() - Date.parse(record.startedAt) < 10 * 60 * 1000;
  const isStopping = record => record?.cleanupPending && (record.state === "stopped" || record.state === "expired");
  function stateText(record, detailed = false) {
    if (isStopping(record)) return "stopping" + (detailed ? " public sharing" : "");
    if (record.state === "starting") return record.phase === "certificate" ? "getting a certificate" + (detailed ? " for the public address" : "") : "starting" + (detailed ? " public sharing" : "");
    return record.public ? until(record) : record.state === "failed" || record.state === "cleanup" ? "needs attention" : record.state;
  }
  const tileFor = id => document.querySelector(`.share-tile[data-share-target="${id}"]`);
  const recordFor = id => records.find(record => record.targetID === id);
  function errorIn(container, message) {
    const el = container.querySelector(":scope > .share-error");
    if (!el) return;
    if (el.textContent !== message) el.textContent = message;
    el.hidden = !message;
  }
  function show(dialog) {
    document.querySelectorAll(".share-dialog[open]").forEach(el => el.close());
    errorIn(dialog, "");
    dialog.showModal();
    updateActions();
  }
  document.querySelectorAll(".share-dialog").forEach(dialog => {
    dialog.querySelector("[data-share-close]").onclick = () => dialog.close();
    dialog.addEventListener("close", () => {
      if (document.querySelector(".share-dialog[open]")) return;
      const tile = tileFor(selected);
      const back = returnFocus?.isConnected ? returnFocus : tile?.querySelector("button.share-globe") || tile?.querySelector(".tile") || summary.querySelector("button");
      back?.focus({ preventScroll: true });
    });
  });
  function deadline() {
    const hours = { "1h": 1, "8h": 8, "24h": 24, "7d": 168 }[confirm.querySelector("input:checked").value];
    byID("share-deadline").textContent = hours ? "Sharing ends " + shortDate(Date.now() + hours * 3600000) + "." : "Public until an admin stops it.";
  }
  confirm.addEventListener("change", deadline);
  function openConfirm(target) {
    selected = target.targetID;
    approvedRevision = target.revision;
    operationID = crypto.randomUUID();
    byID("share-confirm-title").textContent = "Share " + target.label + " publicly?";
    byID("share-origin").textContent = target.origin;
    confirm.querySelector(".share-target span").textContent = target.preview ? "Preview on your tailnet" : "App on your tailnet";
    byID("share-preview-note").hidden = !target.preview;
    confirm.setAttribute("aria-describedby", target.preview ? "share-warning share-preview-note" : "share-warning");
    for (const input of confirm.querySelectorAll('input[name="share-duration"]')) {
      const hidden = target.preview && (input.value === "7d" || input.value === "until-stopped");
      input.parentElement.hidden = hidden;
      input.disabled = hidden;
      input.checked = input.value === (target.preview ? "1h" : "24h");
    }
    deadline();
    show(confirm);
  }
  function renderManage() {
    const record = recordFor(selected);
    if (!record || isStopping(record)) { manage.close(); return false; }
    const terminal = record.canDismiss, starting = record.state === "starting";
    byID("share-manage-title").textContent = record.public ? record.label + " is public" : terminal ? "Sharing for " + record.label : "Sharing " + record.label;
    byID("share-message").hidden = record.public;
    byID("share-message").replaceChildren(...(starting ? [spinner(), " " + record.message] : [record.message]));
    byID("share-next-step").hidden = !terminal;
    byID("share-dns-note").hidden = !freshPublicAddress(record);
    byID("share-public-url").textContent = record.url || "";
    byID("share-copy").hidden = !record.public || !record.url;
    byID("share-copy").dataset.copy = record.url || "";
    byID("share-expiry").textContent = "Public " + until(record);
    byID("share-expiry").hidden = !record.public && !starting;
    byID("share-attribution").hidden = !(record.public || terminal) || !record.createdBy;
    byID("share-attribution").textContent = record.createdBy ? (terminal ? "Created by " : "Shared by ") + record.createdBy + (record.createdAt ? " at " + shortDate(record.createdAt) : "") : "";
    byID("share-review").hidden = !record.public || !!record.expiresAt || !record.nextReviewAt;
    byID("share-review").textContent = record.nextReviewAt ? "Next review on " + calendarDate(record.nextReviewAt) : "";
    byID("share-stop").hidden = terminal;
    byID("share-stop").textContent = record.cleanupPending ? "Retry cleanup" : "Stop sharing";
    byID("share-dismiss").hidden = !terminal;
    byID("share-retry").hidden = !terminal || !targets.has(record.targetID) || !!targets.get(record.targetID).reason;
    return true;
  }
  function openManage(record) {
    selected = record.targetID;
    if (renderManage()) show(manage);
  }
  function openTarget(id, trigger) {
    returnFocus = trigger;
    const record = recordFor(id), target = targets.get(id);
    if (record) { openManage(record); return; }
    if (target.reason) {
      byID("share-ineligible-title").textContent = target.label + " can't be shared yet";
      byID("share-ineligible-reason").textContent = target.reason;
      show(explanation);
    } else openConfirm(target);
  }
  function summaryName(record, container) {
    const tile = tileFor(record.targetID);
    const name = document.createElement(tile || admin ? "button" : "span");
    name.textContent = record.label;
    if (name.tagName === "BUTTON") {
      name.type = "button";
      name.dataset.record = record.id;
      name.onclick = () => {
        if (!tile) { returnFocus = name; openManage(record); return; }
        tile.scrollIntoView({ block: "center", behavior: reducedMotion.matches ? "instant" : "smooth" });
        (tile.querySelector("button.share-globe") || tile.querySelector(".tile")).focus({ preventScroll: true });
        tile.classList.add("summary-highlight");
        setTimeout(() => tile.classList.remove("summary-highlight"), 1800);
      };
    }
    container.append(name);
  }
  function render(data) {
    document.querySelectorAll(".share-error").forEach(el => {
      if (el.textContent === transportMessage) { el.textContent = ""; el.hidden = true; }
    });
    admin = data.admin === true;
    errorIn(root, admin ? data.error || "" : "");
    const snapshot = JSON.stringify(data);
    if (snapshot === lastSnapshot) {
      if (manage.open) byID("share-dns-note").hidden = !freshPublicAddress(recordFor(selected));
      return;
    }
    lastSnapshot = snapshot;
    const focusedSummary = document.activeElement.closest("#share-summary button")?.dataset.record;
    records = data.records || [];
    targets.clear();
    for (const target of data.targets || []) targets.set(target.targetID, target);
    for (const tile of document.querySelectorAll(".share-tile")) {
      const id = tile.dataset.shareTarget, record = recordFor(id), target = targets.get(id);
      const starting = record?.state === "starting", stopping = isStopping(record), pending = starting || stopping;
      const slot = tile.querySelector(".share-slot");
      slot.hidden = (!record && !target) || (!admin && !record?.public && !pending);
      if (slot.hidden) { slot.replaceChildren(); continue; }
      const tag = admin ? "BUTTON" : "SPAN";
      let globe = slot.firstElementChild;
      if (!globe || globe.tagName !== tag) {
        globe = document.createElement(tag);
        globe.className = "share-globe";
        if (admin) { globe.type = "button"; globe.setAttribute("aria-haspopup", "dialog"); }
        else globe.setAttribute("role", "img");
        slot.replaceChildren(globe);
      }
      const variant = record?.public && !pending ? "public" : "outline";
      if (globe.dataset.variant !== variant) {
        globe.innerHTML = shareGlobe(variant === "public");
        globe.dataset.variant = variant;
      }
      globe.classList.toggle("is-public", !!record?.public);
      globe.classList.toggle("is-starting", starting);
      const ineligible = !record && !!target?.reason;
      let label;
      if (pending) label = record.label + ": " + stateText(record, true);
      else if (record?.public) label = record.label + " is public " + stateText(record);
      else if (record) label = "Manage sharing for " + record.label;
      else label = ineligible ? target.label + " cannot be shared: " + target.reason : "Share " + target.label + " publicly";
      globe.setAttribute("aria-label", label); globe.title = label;
      if (admin) {
        if (ineligible || stopping) globe.setAttribute("aria-disabled", "true"); else globe.removeAttribute("aria-disabled");
        if (stopping) { globe.removeAttribute("aria-haspopup"); globe.removeAttribute("aria-controls"); }
        else {
          globe.setAttribute("aria-haspopup", "dialog");
          globe.setAttribute("aria-controls", record ? "share-manage" : ineligible ? "share-ineligible" : "share-confirm");
        }
        globe.onclick = stopping ? null : () => openTarget(id, globe);
      }
    }
    summary.replaceChildren(); summary.hidden = !records.length;
    if (records.length) {
      const icon = document.createElement("span"); icon.className = "summary-globe"; icon.setAttribute("aria-hidden", "true"); icon.innerHTML = shareGlobe(true);
      if (records.some(record => record.state === "starting" || isStopping(record))) {
        icon.classList.add("is-pending");
        icon.append(spinner());
      }
      const text = document.createElement("span");
      text.append("Public sharing: ");
      records.forEach((record, index) => {
        if (index) text.append(" · ");
        summaryName(record, text);
        text.append(" - " + stateText(record));
      });
      summary.append(icon, text);
    }
    const due = records.filter(record => record.reviewDue);
    for (const banner of [...reminders.children]) if (!due.some(record => record.id === banner.dataset.record)) banner.remove();
    for (const record of due) {
      let banner = reminders.querySelector(`[data-record="${record.id}"]`);
      if (!banner) {
        banner = document.createElement("div"); banner.className = "share-reminder"; banner.dataset.record = record.id;
        const error = document.createElement("p"); error.className = "share-error error"; error.setAttribute("role", "alert"); error.hidden = true;
        banner.append(document.createElement("p"), error); reminders.append(banner);
      }
      const days = Math.max(30, Math.floor((Date.now() - new Date(record.createdAt)) / 86400000));
      banner.firstElementChild.textContent = admin ? record.label + " has been public for " + days + " days. Keep sharing?" : record.label + " is due for a sharing review.";
      const actions = banner.querySelector(".share-buttons");
      if (admin && !actions) {
        const controls = document.createElement("div"); controls.className = "share-buttons";
        for (const [label, route] of [["Keep sharing", "review"], ["Stop sharing", "stop"]]) {
          const button = document.createElement("button"); button.type = "button"; button.className = "share-button"; button.textContent = label; button.dataset.route = route;
          controls.append(button);
        }
        banner.append(controls);
      } else if (!admin) { actions?.remove(); errorIn(banner, ""); }
    }
    if (manage.open) renderManage();
    updateActions();
    if (focusedSummary) summary.querySelector(`[data-record="${focusedSummary}"]`)?.focus({ preventScroll: true });
  }
  reminders.onclick = async event => {
    const button = event.target.closest("button[data-route]");
    if (!button) return;
    const banner = button.closest(".share-reminder"), record = records.find(record => record.id === banner.dataset.record);
    if (await mutate(button.dataset.route, { id: record.id, revision: record.revision }, banner)) {
      const tile = tileFor(record.targetID);
      (tile?.querySelector("button.share-globe") || tile?.querySelector(".tile") || summary.querySelector("button"))?.focus({ preventScroll: true });
      if (button.dataset.route === "review") status.textContent = "Sharing review saved for " + record.label + ".";
    }
  };
  async function mutate(route, body, context) {
    if (busy || !admin) return false;
    const action = document.activeElement, retryCleanup = route === "stop" && records.find(record => record.id === body.id)?.cleanupPending;
    let succeeded = false, data;
    generation++;
    busy = true; context.setAttribute("aria-busy", "true"); errorIn(context, "");
    updateActions();
    try {
      const response = await fetch("/share/" + route, { method: "POST", headers: { "Content-Type": "application/json", "X-Tailhome-Share-Request": "1" }, body: JSON.stringify(body), cache: "no-store" });
      data = await response.json().catch(() => null);
      if (!response.ok || !data) { errorIn(context, data?.error || "Request failed (" + response.status + ")."); return false; }
      if (route === "stop" || route === "dismiss") {
        manage.close();
        status.textContent = route === "stop" ? retryCleanup ? "Cleanup retried." : "Public sharing stopped." : "Sharing record dismissed.";
      }
      render(data);
      succeeded = true;
      return true;
    } catch {
      errorIn(context, transportMessage);
      return false;
    } finally {
      busy = false; context.removeAttribute("aria-busy");
      updateActions();
      if (action?.isConnected && !action.disabled && !action.hidden && context.isConnected) action.focus({ preventScroll: true });
      if ((succeeded && (route === "create" || route === "stop")) || data?.code === "revision") void reconcile();
    }
  }
  byID("share-create").onclick = async () => {
    if (await mutate("create", { targetID: selected, revision: approvedRevision, duration: confirm.querySelector("input:checked").value, operationID }, confirm)) confirm.close();
  };
  byID("share-stop").onclick = () => {
    const record = recordFor(selected);
    if (record) mutate("stop", { id: record.id, revision: record.revision }, manage);
  };
  byID("share-dismiss").onclick = () => {
    const record = recordFor(selected);
    if (record) mutate("dismiss", { id: record.id, revision: record.revision }, manage);
  };
  byID("share-retry").onclick = () => openConfirm(targets.get(selected));
  render(JSON.parse(byID("share-initial").textContent));
  async function reconcile() {
    if (busy || document.visibilityState !== "visible") return;
    const request = ++generation;
    try {
      const response = await fetch("/share/status", { cache: "no-store" });
      if (!response.ok) throw new Error();
      const data = await response.json();
      if (request === generation) render(data);
    } catch {
      if (request === generation && admin) errorIn(manage.open ? manage : root, transportMessage);
    }
  }
  async function poll() {
    await reconcile();
    setTimeout(poll, admin ? 5000 : 30000);
  }
  setTimeout(poll, admin ? 5000 : 30000);
}
