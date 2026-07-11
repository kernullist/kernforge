"use strict";

const state = {
  csrf: "",
  sessionProof: "",
  bootstrap: null,
  decisions: [],
  profile: null,
  profileStatus: null,
  scopeMode: "current",
  projectID: "",
  decisionTotal: 0,
  nextCursor: "",
  selectedID: "",
  selectedRecord: null,
  lastFocusedDecisionID: "",
  view: "journal",
  searchTimer: 0,
  bootstrapRequestGeneration: 0,
  decisionRequestGeneration: 0,
  decisionAbortController: null,
  selectionRequestGeneration: 0,
  profileRequestGeneration: 0,
  profileMutation: Promise.resolve(),
};

const $ = (id) => document.getElementById(id);

function node(tag, className, text) {
  const element = document.createElement(tag);
  if (className) {
    element.className = className;
  }
  if (text !== undefined && text !== null) {
    element.textContent = String(text);
  }
  return element;
}

function clear(element) {
  while (element.firstChild) {
    element.removeChild(element.firstChild);
  }
}

function append(parent, ...children) {
  for (const child of children) {
    if (child) {
      parent.appendChild(child);
    }
  }
  return parent;
}

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body !== undefined) {
    headers.set("Content-Type", "application/json");
  }
  if (state.csrf && options.method && options.method !== "GET") {
    headers.set("X-CSRF-Token", state.csrf);
  }
  if (state.sessionProof && path.startsWith("/api/") && path !== "/api/auth/exchange") {
    headers.set("X-KernForge-Session", state.sessionProof);
  }
  const response = await fetch(path, { ...options, headers, credentials: "same-origin" });
  const contentType = response.headers.get("Content-Type") || "";
  let payload = null;
  if (contentType.includes("application/json")) {
    payload = await response.json();
  } else {
    payload = await response.text();
  }
  if (!response.ok) {
    const message = payload && payload.error ? payload.error : String(payload || `HTTP ${response.status}`);
    const error = new Error(message);
    error.status = response.status;
    throw error;
  }
  return payload;
}

async function authenticate() {
  const fragment = new URLSearchParams(window.location.hash.replace(/^#/, ""));
  const token = fragment.get("bootstrap");
  if (!token) {
    try {
      const stored = JSON.parse(sessionStorage.getItem("kernforge-decision-session") || "null");
      state.csrf = (stored && stored.csrf) || "";
      state.sessionProof = (stored && stored.request) || "";
    } catch (_) {
      state.csrf = "";
      state.sessionProof = "";
    }
    return;
  }
  const payload = await api("/api/auth/exchange", {
    method: "POST",
    body: JSON.stringify({ token }),
  });
  state.csrf = payload.csrf_token || "";
  state.sessionProof = payload.request_token || "";
  try {
    sessionStorage.setItem("kernforge-decision-session", JSON.stringify({ csrf: state.csrf, request: state.sessionProof }));
  } catch (_) {
    // The active page remains authenticated even when session storage is unavailable.
  }
  history.replaceState(null, "", window.location.pathname + window.location.search);
}

async function initialize() {
  bindEvents();
  try {
    await authenticate();
    await loadBootstrap();
    await loadDecisions();
  } catch (error) {
    if (error.status === 401) {
      try { sessionStorage.removeItem("kernforge-decision-session"); } catch (_) {}
      $("auth-gate").hidden = false;
      return;
    }
    showToast(error.message, true);
  }
}

async function loadBootstrap() {
  const generation = ++state.bootstrapRequestGeneration;
  const payload = await api("/api/bootstrap");
  if (generation !== state.bootstrapRequestGeneration) return;
  state.bootstrap = payload;
  state.csrf = payload.csrf_token || state.csrf;
  state.profileStatus = payload.profile || null;
  if (state.scopeMode === "current") {
    state.projectID = (payload.workspace && payload.workspace.project_id) || "";
  }
  $("workspace-alias").textContent = payload.workspace.project_alias || "Workspace";
  $("workspace-path").textContent = payload.workspace.path || "";
  $("nav-decision-count").textContent = String(payload.counts.active || 0);
  $("nav-profile-count").textContent = String(payload.counts.profile_rules || 0);
  renderProjects(payload.projects || []);
  renderProjectSelector(payload.projects || []);
  populateSelect($("domain-filter"), "All domains", payload.domains || []);
  populateSelect($("kind-filter"), "All decision kinds", payload.kinds || []);
  renderIssues(payload.issues || [], payload.profile || {});
}

function populateSelect(select, emptyLabel, values) {
  const current = select.value;
  clear(select);
  const empty = node("option", "", emptyLabel);
  empty.value = "";
  select.appendChild(empty);
  for (const value of values) {
    const option = node("option", "", value);
    option.value = value;
    select.appendChild(option);
  }
  if ([...select.options].some((option) => option.value === current)) {
    select.value = current;
  }
}

function renderProjects(projects) {
  const list = $("project-list");
  clear(list);
  for (const project of projects) {
    const button = node("button", "project-button");
    button.type = "button";
    button.dataset.projectId = project.id;
    if (state.projectID === project.id) {
      button.classList.add("active");
    }
    button.setAttribute("aria-pressed", String(state.scopeMode !== "all" && state.projectID === project.id));
    append(
      button,
      node("span", "project-dot"),
      node("span", "", project.alias || project.id),
      node("span", "project-id", String(project.id || "").slice(-6)),
    );
    button.addEventListener("click", async () => {
      state.scopeMode = "project";
      state.projectID = project.id;
      renderProjects(projects);
      renderProjectSelector(projects);
      await loadDecisions();
    });
    list.appendChild(button);
  }
  $("scope-all").setAttribute("aria-pressed", String(state.scopeMode === "all"));
}

function renderProjectSelector(projects) {
  const select = $("project-filter");
  clear(select);
  const workspace = (state.bootstrap && state.bootstrap.workspace) || {};
  const current = node("option", "", `Current: ${workspace.project_alias || "workspace"}`);
  current.value = `current:${workspace.project_id || ""}`;
  select.appendChild(current);
  const all = node("option", "", "All projects");
  all.value = "all";
  select.appendChild(all);
  for (const project of projects) {
    const option = node("option", "", project.alias || project.id);
    option.value = `project:${project.id}`;
    select.appendChild(option);
  }
  if (state.scopeMode === "all") {
    select.value = "all";
  } else if (state.scopeMode === "current") {
    select.value = current.value;
  } else {
    select.value = `project:${state.projectID}`;
  }
}

function renderIssues(issues, profileStatus = {}) {
  const banner = $("issue-banner");
  const profileDirty = Boolean(profileStatus.dirty || profileStatus.error);
  if (!issues.length && !profileDirty) {
    banner.hidden = true;
    return;
  }
  banner.hidden = false;
  if (issues.length) {
    $("issue-title").textContent = `${issues.length} journal file${issues.length === 1 ? "" : "s"} could not be read`;
    const first = issues[0];
    const profileCopy = profileDirty ? " The preference profile is also stale; rebuild it after repairing the journal." : "";
    $("issue-copy").textContent = `${first.file}: ${first.error}. Healthy records remain available; repair or remove the damaged file before profile rebuild or export.${profileCopy}`;
    return;
  }
  $("issue-title").textContent = "Preference profile needs attention";
  $("issue-copy").textContent = `${profileStatus.error || "The last automatic profile rebuild did not complete."} Source decisions remain intact; use Rebuild profile to retry.`;
}

async function loadDecisions(appendPage = false) {
  if (appendPage && !state.nextCursor) return;
  const generation = ++state.decisionRequestGeneration;
  if (state.decisionAbortController) {
    state.decisionAbortController.abort();
  }
  const controller = new AbortController();
  state.decisionAbortController = controller;
  if (!appendPage) {
    state.nextCursor = "";
    $("load-more").hidden = true;
  }
  const params = new URLSearchParams();
  if (state.projectID) params.set("project_id", state.projectID);
  if ($("domain-filter").value) params.set("domain", $("domain-filter").value);
  if ($("kind-filter").value) params.set("decision_kind", $("kind-filter").value);
  if ($("decision-search").value.trim()) params.set("q", $("decision-search").value.trim());
  if ($("deleted-filter").checked) params.set("include_deleted", "true");
  params.set("limit", "100");
  if (appendPage) params.set("cursor", state.nextCursor);
  let payload;
  try {
    payload = await api(`/api/decisions?${params.toString()}`, { signal: controller.signal });
  } catch (error) {
    if (error && error.name === "AbortError") return;
    throw error;
  } finally {
    if (state.decisionAbortController === controller) {
      state.decisionAbortController = null;
    }
  }
  if (generation !== state.decisionRequestGeneration) return;
  const records = payload.records || [];
  if (appendPage) {
    const existing = new Set(state.decisions.map((record) => record.id));
    state.decisions = state.decisions.concat(records.filter((record) => !existing.has(record.id)));
  } else {
    state.decisions = records;
  }
  state.decisionTotal = Number.isFinite(payload.total) ? payload.total : state.decisions.length;
  state.nextCursor = payload.next_cursor || "";
  renderDecisionList();
  if (state.selectedID) {
    const stillVisible = state.decisions.some((record) => record.id === state.selectedID);
    if (!stillVisible) {
      state.selectedID = "";
      state.selectedRecord = null;
      renderInspectorEmpty();
    }
  }
}

function renderDecisionList() {
  const list = $("decision-list");
  clear(list);
  const loaded = state.decisions.length;
  const total = state.decisionTotal;
  $("journal-summary").textContent = `${loaded} of ${total} decision${total === 1 ? "" : "s"} loaded in the current scope`;
  $("load-more").hidden = !state.nextCursor;
  $("decision-empty").hidden = state.decisions.length !== 0;
  for (const record of state.decisions) {
    const row = node("button", "decision-row");
    row.type = "button";
    row.dataset.decisionId = record.id;
    if (record.id === state.selectedID) row.classList.add("active");
    const main = node("div", "decision-row-main");
    const meta = node("div", "row-meta");
    append(meta, badge(record.decision_kind || "decision", "kind-badge"));
    if (record.risk_level) append(meta, badge(record.risk_level, "risk-badge"));
    if (record.status === "deleted") append(meta, badge("deleted", "status-badge deleted"));
    const problem = node("p", "decision-problem", record.problem);
    const choice = node("p", "decision-choice");
    append(choice, node("span", "", "Selected: "), node("strong", "", selectedLabel(record)));
    append(main, meta, problem, choice);
    const time = node("time", "decision-time", formatTime(record.updated_at));
    time.dateTime = record.updated_at || "";
    append(row, main, time);
    row.addEventListener("click", () => selectDecision(record.id, row));
    list.appendChild(row);
  }
}

function badge(text, className) {
  return node("span", className, text || "-");
}

function selectedLabel(record) {
  if (record.custom_selection) return record.custom_selection;
  const option = (record.options || []).find((item) => item.id === record.selected_option_id);
  return option ? option.label : (record.selected_option_id || "Unknown");
}

function formatTime(value) {
  if (!value) return "Unknown";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString([], { year: "numeric", month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

async function selectDecision(id, sourceElement = null, moveFocus = true) {
  const generation = ++state.selectionRequestGeneration;
  if (sourceElement) state.lastFocusedDecisionID = id;
  try {
    const record = await api(`/api/decisions/${encodeURIComponent(id)}`);
    if (generation !== state.selectionRequestGeneration) return;
    state.selectedID = id;
    state.selectedRecord = record;
    renderDecisionList();
    renderInspector(record);
    $("inspector").classList.add("open");
    syncInspectorSemantics();
    if (moveFocus && window.matchMedia("(max-width: 980px)").matches) {
      const close = $("inspector").querySelector("[data-inspector-close]");
      if (close) close.focus();
    }
  } catch (error) {
    showToast(error.message, true);
  }
}

function renderInspectorEmpty(clearSelection = false, returnFocus = false) {
  if (clearSelection) {
    state.selectionRequestGeneration++;
    state.selectedID = "";
    state.selectedRecord = null;
    renderDecisionList();
  }
  $("inspector-empty").hidden = false;
  $("inspector-content").hidden = true;
  $("inspector").classList.remove("open");
  syncInspectorSemantics();
  if (returnFocus && state.lastFocusedDecisionID) {
    const row = document.querySelector(`[data-decision-id="${CSS.escape(state.lastFocusedDecisionID)}"]`);
    if (row) row.focus();
  }
}

function syncInspectorSemantics() {
  const inspector = $("inspector");
  const mobile = window.matchMedia("(max-width: 980px)").matches;
  const open = inspector.classList.contains("open");
  $("inspector-backdrop").hidden = !(mobile && open);
  if (mobile && open) {
    inspector.setAttribute("role", "dialog");
    inspector.setAttribute("aria-modal", "true");
  } else {
    inspector.removeAttribute("role");
    inspector.removeAttribute("aria-modal");
  }
  const rail = document.querySelector(".scope-rail");
  const workspace = document.querySelector(".journal-workspace");
  if (rail) rail.inert = mobile && open;
  if (workspace) workspace.inert = mobile && open;
}

function renderInspector(record) {
  const container = $("inspector-content");
  clear(container);
  $("inspector-empty").hidden = true;
  container.hidden = false;

  const header = node("header", "inspector-header");
  const titleRow = node("div", "inspector-title-row");
  const titleBlock = node("div");
  append(titleBlock, node("p", "eyebrow", `REVISION ${record.revision}`), node("h2", "", record.problem));
  const closeButton = node("button", "text-button", "Close");
  closeButton.type = "button";
  closeButton.dataset.inspectorClose = "true";
  closeButton.addEventListener("click", () => renderInspectorEmpty(true, true));
  append(titleRow, titleBlock, closeButton);
  const meta = node("div", "inspector-meta");
  append(meta, badge(record.decision_kind || "decision", "kind-badge"));
  if (record.risk_level) append(meta, badge(record.risk_level, "risk-badge"));
  append(meta, badge(record.project_alias || record.project_id || "project", "scope-badge"));
  if (record.status === "deleted") append(meta, badge("deleted", "status-badge deleted"));
  const actions = node("div", "inspector-actions");
  const edit = actionButton("Edit", "secondary-button", () => renderDecisionEdit(record));
  const history = actionButton("History", "secondary-button", () => loadHistory(record));
  append(actions, edit, history);
  if (record.status === "deleted") {
    append(actions, actionButton("Restore", "primary-button", () => restoreDecision(record)));
  } else {
    append(actions, actionButton("Delete", "secondary-button danger-button", () => deleteDecision(record)));
  }
  append(header, titleRow, meta, actions);
  container.appendChild(header);

  const forkSection = inspectorSection("Decision fork");
  const fork = node("div", "decision-fork");
  for (const option of record.options || []) {
    fork.appendChild(renderForkOption(record, option));
  }
  if (record.custom_selection) {
    const custom = node("article", "fork-option selected");
    append(custom, node("h4", "", record.custom_selection), node("p", "", "User-provided alternative"));
    fork.appendChild(custom);
  }
  forkSection.appendChild(fork);
  container.appendChild(forkSection);

  const rationaleSection = inspectorSection("Rationale ledger");
  const selected = node("div", "rationale-block");
  append(selected, node("p", "", record.selection_reason_raw || "No selection rationale recorded."));
  rationaleSection.appendChild(selected);
  const rejectionList = node("div", "rejection-list");
  const labels = new Map((record.options || []).map((option) => [option.id, option.label]));
  for (const rejection of record.rejected_reasons || []) {
    const item = node("div", "rejection-item");
    append(item, node("strong", "", labels.get(rejection.option_id) || rejection.option_id), node("p", "", rejection.reason || "No reason recorded."));
    rejectionList.appendChild(item);
  }
  rationaleSection.appendChild(rejectionList);
  container.appendChild(rationaleSection);

  const evidenceSection = inspectorSection("Provenance and scope");
  const grid = node("div", "provenance-grid");
  const provenance = record.provenance || {};
  append(
    grid,
    provenanceCell("Selection", provenance.selection || "unknown"),
    provenanceCell("Rationale", provenance.rationale || "unknown"),
    provenanceCell("Confidence", `${Math.round((record.detector_confidence || 0) * 100)}%`),
    provenanceCell("Updated", formatTime(record.updated_at)),
  );
  evidenceSection.appendChild(grid);
  if ((record.domains || []).length || (record.tags || []).length) {
    const tags = node("div", "tag-list");
    for (const value of [...(record.domains || []), ...(record.tags || [])]) tags.appendChild(node("span", "tag", value));
    evidenceSection.appendChild(tags);
  }
  container.appendChild(evidenceSection);
}

function renderUpdatedDecision(record) {
  if (!state.decisions.some((item) => item.id === record.id)) {
    renderInspectorEmpty(true, false);
    return;
  }
  state.selectedID = record.id;
  state.selectedRecord = record;
  renderDecisionList();
  renderInspector(record);
  $("inspector").classList.add("open");
  syncInspectorSemantics();
}

function renderForkOption(record, option) {
  const selected = record.selected_option_id === option.id;
  const card = node("article", `fork-option${selected ? " selected" : ""}${option.recommended ? " recommended" : ""}`);
  append(card, node("h4", "", option.label));
  const flags = node("div", "option-flags");
  if (selected) flags.appendChild(node("span", "mini-flag", "Selected"));
  if (option.recommended) flags.appendChild(node("span", "mini-flag", "Recommended"));
  if (flags.childNodes.length) card.appendChild(flags);
  append(card, node("p", "", option.description || "No description recorded."));
  if ((option.pros || []).length) append(card, node("p", "", `Pros: ${option.pros.join("; ")}`));
  if ((option.cons || []).length) append(card, node("p", "", `Cons: ${option.cons.join("; ")}`));
  return card;
}

function inspectorSection(title) {
  const section = node("section", "inspector-section");
  section.appendChild(node("h3", "", title));
  return section;
}

function provenanceCell(label, value) {
  const cell = node("div", "provenance-cell");
  append(cell, node("span", "", label), node("strong", "", value));
  return cell;
}

function actionButton(label, className, handler) {
  const button = node("button", className, label);
  button.type = "button";
  button.addEventListener("click", handler);
  return button;
}

function renderDecisionEdit(record) {
  const container = $("inspector-content");
  clear(container);
  const header = node("header", "inspector-header");
  append(header, node("p", "eyebrow", `EDIT REVISION ${record.revision}`), node("h2", "", record.problem));
  container.appendChild(header);
  const section = inspectorSection("Editable rationale and scope");
  const form = node("form", "edit-form");

  const selection = labeledSelect("Selected approach", "edit-selection");
  for (const option of record.options || []) {
    const item = node("option", "", option.label);
    item.value = option.id;
    selection.control.appendChild(item);
  }
  const customOption = node("option", "", "Other / custom approach");
  customOption.value = "__custom__";
  selection.control.appendChild(customOption);
  selection.control.value = record.custom_selection ? "__custom__" : record.selected_option_id;
  form.appendChild(selection.label);

  const custom = labeledInput("Custom approach", "edit-custom", record.custom_selection || "");
  custom.label.hidden = selection.control.value !== "__custom__";
  form.appendChild(custom.label);
  selection.control.addEventListener("change", () => {
    custom.label.hidden = selection.control.value !== "__custom__";
    updateRejectionInputs();
  });

  const reason = labeledTextarea("Why this approach was selected", "edit-selection-reason", record.selection_reason_raw || "");
  reason.control.required = true;
  form.appendChild(reason.label);
  const rejectionMap = new Map((record.rejected_reasons || []).map((item) => [item.option_id, item.reason || ""]));
  const rejectionControls = [];
  for (const option of record.options || []) {
    const control = labeledTextarea(`Why ${option.label} was not selected`, `reject-${option.id}`, rejectionMap.get(option.id) || "");
    control.label.dataset.optionId = option.id;
    rejectionControls.push(control);
    form.appendChild(control.label);
  }
  function updateRejectionInputs() {
    custom.control.required = selection.control.value === "__custom__";
    for (const control of rejectionControls) {
      const selected = selection.control.value === control.label.dataset.optionId;
      control.label.hidden = selected;
      control.control.disabled = selected;
      control.control.required = !selected;
    }
  }
  updateRejectionInputs();

  const grid = node("div", "form-grid");
  const kind = labeledInput("Decision kind", "edit-kind", record.decision_kind || "");
  const risk = labeledSelect("Risk", "edit-risk");
  for (const value of ["", "low", "medium", "high", "critical"]) {
    const item = node("option", "", value || "Unspecified");
    item.value = value;
    risk.control.appendChild(item);
  }
  risk.control.value = record.risk_level || "";
  append(grid, kind.label, risk.label);
  form.appendChild(grid);
  const domains = labeledInput("Domains (comma-separated)", "edit-domains", (record.domains || []).join(", "));
  const tags = labeledInput("Tags (comma-separated)", "edit-tags", (record.tags || []).join(", "));
  const alias = labeledInput("Project alias", "edit-project-alias", record.project_alias || "");
  append(form, domains.label, tags.label, alias.label);

  const actions = node("div", "form-actions");
  append(actions, actionButton("Cancel", "secondary-button", () => renderInspector(record)));
  const save = actionButton("Save revision", "primary-button", async () => {
    if (!form.reportValidity()) return;
    const selectedID = selection.control.value;
    const rejected = [];
    for (const control of rejectionControls) {
      const optionID = control.label.dataset.optionId;
      if (selectedID !== optionID) rejected.push({ option_id: optionID, reason: control.control.value.trim(), source: "user" });
    }
    const patch = {
      expected_revision: record.revision,
      selected_option_id: selectedID === "__custom__" ? "" : selectedID,
      custom_selection: selectedID === "__custom__" ? custom.control.value.trim() : "",
      selection_reason_raw: reason.control.value.trim(),
      rejected_reasons: rejected,
      decision_kind: kind.control.value.trim(),
      risk_level: risk.control.value,
      domains: splitList(domains.control.value),
      tags: splitList(tags.control.value),
      project_alias: alias.control.value.trim(),
    };
    try {
      const updated = await api(`/api/decisions/${encodeURIComponent(record.id)}`, { method: "PATCH", body: JSON.stringify(patch) });
      state.selectedRecord = updated;
      showToast(`Saved revision ${updated.revision}`);
      await Promise.all([loadBootstrap(), loadDecisions()]);
      renderUpdatedDecision(updated);
    } catch (error) {
      showToast(error.status === 409 ? "This decision changed in another dashboard. Refresh and retry." : error.message, true);
      if (error.status === 409) {
        await selectDecision(record.id, null, false);
      }
    }
  });
  actions.appendChild(save);
  form.appendChild(actions);
  section.appendChild(form);
  container.appendChild(section);
}

function labeledInput(labelText, id, value) {
  const label = node("label");
  const title = node("span", "", labelText);
  const control = node("input");
  control.id = id;
  control.value = value || "";
  append(label, title, control);
  return { label, control };
}

function labeledTextarea(labelText, id, value) {
  const label = node("label");
  const title = node("span", "", labelText);
  const control = node("textarea");
  control.id = id;
  control.value = value || "";
  append(label, title, control);
  return { label, control };
}

function labeledSelect(labelText, id) {
  const label = node("label");
  const title = node("span", "", labelText);
  const control = node("select");
  control.id = id;
  append(label, title, control);
  return { label, control };
}

function splitList(value) {
  return [...new Set(String(value || "").split(",").map((item) => item.trim()).filter(Boolean))];
}

async function loadHistory(record) {
  const generation = state.selectionRequestGeneration;
  try {
    const payload = await api(`/api/decisions/${encodeURIComponent(record.id)}/history`);
    if (generation !== state.selectionRequestGeneration || state.selectedID !== record.id) return;
    const current = state.selectedRecord;
    if (!current || current.id !== record.id) return;
    renderInspector(current);
    const section = inspectorSection("Revision history");
    const list = node("div", "history-list");
    for (const revision of payload.records || []) {
      const item = node("div", "history-item");
      append(item, node("strong", "", `Revision ${revision.revision} / ${revision.status}`), node("p", "", `${formatTime(revision.updated_at)} — ${selectedLabel(revision)}`));
      list.appendChild(item);
    }
    section.appendChild(list);
    $("inspector-content").appendChild(section);
    section.scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (error) {
    showToast(error.message, true);
  }
}

async function deleteDecision(record) {
  if (!window.confirm("Soft-delete this decision? Its revision history will remain available.")) return;
  try {
    const updated = await api(`/api/decisions/${encodeURIComponent(record.id)}`, {
      method: "DELETE",
      body: JSON.stringify({ expected_revision: record.revision }),
    });
    showToast("Decision moved to deleted records");
    state.selectedRecord = updated;
    await Promise.all([loadBootstrap(), loadDecisions()]);
    renderUpdatedDecision(updated);
  } catch (error) {
    showToast(error.message, true);
    if (error.status === 409) {
      await selectDecision(record.id, null, false);
    }
  }
}

async function restoreDecision(record) {
  try {
    const updated = await api(`/api/decisions/${encodeURIComponent(record.id)}/restore`, {
      method: "POST",
      body: JSON.stringify({ expected_revision: record.revision }),
    });
    showToast("Decision restored");
    state.selectedRecord = updated;
    await Promise.all([loadBootstrap(), loadDecisions()]);
    renderUpdatedDecision(updated);
  } catch (error) {
    showToast(error.message, true);
    if (error.status === 409) {
      await selectDecision(record.id, null, false);
    }
  }
}

async function loadProfile() {
  const generation = ++state.profileRequestGeneration;
  try {
    const profile = await api("/api/profiles");
    if (generation !== state.profileRequestGeneration) return;
    state.profile = profile;
    renderProfile();
  } catch (error) {
    if (generation !== state.profileRequestGeneration) return;
    showToast(error.message, true);
    state.profile = { revision: 0, rules: [] };
    renderProfile();
  }
}

function renderProfile() {
  const list = $("profile-list");
  clear(list);
  const rules = (state.profile && state.profile.rules) || [];
  $("profile-empty").hidden = rules.length !== 0;
  $("nav-profile-count").textContent = String(rules.length);
  for (const rule of rules) {
    const row = node("article", "profile-rule");
    append(row, node("div", "rule-scope", rule.scope));
    const main = node("div", "rule-main");
    const scopeContext = rule.scope_key && rule.scope !== "global" ? `${rule.scope_key} · ` : "";
    append(main, node("strong", "", rule.preference_label || rule.preference), node("span", "", `${scopeContext}${rule.criterion} · ${rule.support_decision_ids.length} supporting decisions · ${rule.contradiction_decision_ids ? rule.contradiction_decision_ids.length : 0} contradictions`));
    row.appendChild(main);
    row.appendChild(node("div", "rule-confidence", `${Math.round((rule.confidence || 0) * 100)}% confidence`));
    const actions = node("div", "rule-actions");
    const enabled = ruleCheckbox("Enabled", rule.enabled);
    const pinned = ruleCheckbox("Pinned", rule.pinned);
    append(actions, enabled.label, pinned.label);
    row.appendChild(actions);
    const note = node("div", "rule-note");
    const input = node("input");
    input.value = rule.user_note || "";
    input.placeholder = "Add a private note about when this rule should apply";
    input.setAttribute("aria-label", `Private note for ${rule.preference_label || rule.preference}`);
    const save = actionButton("Save note", "secondary-button", () => updateProfileRule(rule.id, { user_note: input.value.trim() }));
    append(note, input, save);
    row.appendChild(note);
    enabled.input.addEventListener("change", () => updateProfileRule(rule.id, { enabled: enabled.input.checked }));
    pinned.input.addEventListener("change", () => updateProfileRule(rule.id, { pinned: pinned.input.checked }));
    list.appendChild(row);
  }
}

function ruleCheckbox(labelText, checked) {
  const label = node("label", "rule-toggle");
  const input = node("input");
  input.type = "checkbox";
  input.checked = Boolean(checked);
  append(label, input, node("span", "", labelText));
  return { label, input };
}

function updateProfileRule(ruleID, patch) {
  state.profileMutation = state.profileMutation
    .catch(() => {})
    .then(() => performProfileRuleUpdate(ruleID, patch));
  return state.profileMutation;
}

async function performProfileRuleUpdate(ruleID, patch) {
  if (!state.profile || !state.profile.revision) {
    showToast("Rebuild the profile before editing rules.", true);
    return;
  }
  const generation = ++state.profileRequestGeneration;
  try {
    const profile = await api(`/api/profiles/${encodeURIComponent(ruleID)}`, {
      method: "PATCH",
      body: JSON.stringify({ expected_revision: state.profile.revision, ...patch }),
    });
    if (generation !== state.profileRequestGeneration) return;
    state.profile = profile;
    showToast("Preference rule updated");
    renderProfile();
  } catch (error) {
    showToast(error.status === 409 ? "The profile changed in another dashboard. Refresh and retry." : error.message, true);
    await loadProfile();
  }
}

function rebuildProfile() {
  state.profileMutation = state.profileMutation
    .catch(() => {})
    .then(performProfileRebuild);
  return state.profileMutation;
}

async function performProfileRebuild() {
  const generation = ++state.profileRequestGeneration;
  try {
    const profile = await api("/api/profiles/rebuild", { method: "POST", body: JSON.stringify({}) });
    if (generation !== state.profileRequestGeneration) return;
    state.profile = profile;
    showToast("Preference profile rebuilt from decision evidence");
    await loadBootstrap();
    renderProfile();
  } catch (error) {
    showToast(error.message, true);
  }
}

function switchView(view) {
  state.view = view;
  for (const button of document.querySelectorAll(".nav-item")) {
    const active = button.dataset.view === view;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
    if (active) button.setAttribute("aria-current", "page");
    else button.removeAttribute("aria-current");
  }
  const profile = view === "preferences";
  $("journal-view").hidden = profile;
  $("profile-view").hidden = !profile;
  $("rebuild-profile").hidden = !profile;
  $("view-kicker").textContent = profile ? "DERIVED JUDGMENT PROFILE" : "CROSS-PROJECT EVIDENCE";
  $("view-title").textContent = profile ? "Preference profile" : "Implementation decisions";
  $("view-subtitle").textContent = profile
    ? "Repeated choices promoted into transparent, reversible rules."
    : "The choice, its rationale, and the alternatives that were ruled out.";
  if (profile) {
    renderInspectorEmpty(true, false);
    loadProfile();
  }
}

async function downloadExport() {
  const request = {
    project_id: state.projectID,
    domain: $("domain-filter").value,
    decision_kind: $("kind-filter").value,
    query: $("decision-search").value.trim(),
    include_deleted: $("export-deleted").checked,
    include_private_metadata: $("export-private").checked,
  };
  try {
    const response = await fetch("/api/export", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": state.csrf,
        "X-KernForge-Session": state.sessionProof,
      },
      body: JSON.stringify(request),
    });
    if (!response.ok) {
      const payload = await response.json().catch(() => ({ error: `HTTP ${response.status}` }));
      throw new Error(payload.error || `HTTP ${response.status}`);
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = node("a");
    link.href = url;
    const disposition = response.headers.get("Content-Disposition") || "";
    const match = disposition.match(/filename="([^"]+)"/);
    link.download = match ? match[1] : "kernforge-decisions.json";
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
    $("export-dialog").close();
    showToast("Decision journal exported");
  } catch (error) {
    showToast(error.message, true);
  }
}

function bindEvents() {
  for (const button of document.querySelectorAll(".nav-item")) {
    button.addEventListener("click", () => switchView(button.dataset.view));
  }
  $("scope-all").addEventListener("click", async () => {
    state.scopeMode = "all";
    state.projectID = "";
    renderProjects((state.bootstrap && state.bootstrap.projects) || []);
    renderProjectSelector((state.bootstrap && state.bootstrap.projects) || []);
    await loadDecisions();
  });
  $("project-filter").addEventListener("change", async () => {
    const value = $("project-filter").value;
    if (value === "all") {
      state.scopeMode = "all";
      state.projectID = "";
    } else if (value.startsWith("current:")) {
      state.scopeMode = "current";
      state.projectID = value.slice("current:".length);
    } else {
      state.scopeMode = "project";
      state.projectID = value.slice("project:".length);
    }
    renderProjects((state.bootstrap && state.bootstrap.projects) || []);
    await loadDecisions();
  });
  $("domain-filter").addEventListener("change", () => loadDecisions());
  $("kind-filter").addEventListener("change", () => loadDecisions());
  $("deleted-filter").addEventListener("change", () => loadDecisions());
  $("decision-search").addEventListener("input", () => {
    window.clearTimeout(state.searchTimer);
    state.searchTimer = window.setTimeout(() => loadDecisions(), 180);
  });
  $("refresh-button").addEventListener("click", async () => {
    const selectedID = state.selectedID;
    await loadBootstrap();
    if (state.view === "preferences") await loadProfile();
    else {
      await loadDecisions();
      if (selectedID && state.decisions.some((record) => record.id === selectedID)) {
        await selectDecision(selectedID, null, false);
      }
    }
    showToast("Dashboard refreshed");
  });
  $("load-more").addEventListener("click", () => loadDecisions(true));
  $("inspector-backdrop").addEventListener("click", () => renderInspectorEmpty(true, true));
  $("rebuild-profile").addEventListener("click", rebuildProfile);
  $("export-open").addEventListener("click", () => {
    const dialog = $("export-dialog");
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
  });
  $("export-private").addEventListener("change", () => {
    $("export-warning").hidden = !$("export-private").checked;
  });
  $("export-download").addEventListener("click", downloadExport);
  window.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && $("inspector").classList.contains("open")) {
      renderInspectorEmpty(true, true);
      return;
    }
    if (event.key === "Tab" && window.matchMedia("(max-width: 980px)").matches && $("inspector").classList.contains("open")) {
      trapInspectorFocus(event);
    }
  });
  window.addEventListener("resize", syncInspectorSemantics);
}

function trapInspectorFocus(event) {
  const focusable = [...$("inspector").querySelectorAll("button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex='-1'])")]
    .filter((element) => !element.hidden && element.getClientRects().length > 0);
  if (!focusable.length) {
    event.preventDefault();
    $("inspector").focus();
    return;
  }
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

let toastTimer = 0;
function showToast(message, isError = false) {
  const toast = $("toast");
  toast.textContent = message;
  toast.classList.toggle("error", isError);
  toast.hidden = false;
  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => { toast.hidden = true; }, 4200);
}

document.addEventListener("DOMContentLoaded", initialize);
