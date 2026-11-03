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
async function copy(value) {
  try { await navigator.clipboard.writeText(value); return true; } catch {}
  // The Clipboard API needs HTTPS; this older path also works on plain HTTP, during a click.
  const back = document.activeElement; // select() moves focus; give it back after
  const ta = Object.assign(document.createElement("textarea"), { value });
  ta.style.cssText = "position:fixed;opacity:0";
  document.body.append(ta);
  ta.select();
  let ok = false;
  try { ok = document.execCommand("copy"); } catch {}
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
  if (await copy(value)) {
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
