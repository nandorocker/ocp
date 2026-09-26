const token = document.querySelector('meta[name="ocp-token"]').content;
const app = document.querySelector('#app');
const workspace = document.querySelector('#workspace');
const profileList = document.querySelector('#profile-list');
const agentsNav = document.querySelector('#agents-nav');
const sidebar = document.querySelector('#sidebar');
const dialog = document.querySelector('#dialog');
const dialogForm = document.querySelector('#dialog-form');
let state, view = { type: 'profile', name: '' }, dirty = false, draft, baseline, preview, models, modelsPromise;
let toastTimer, previewTimer, previewRequest = 0;

async function api(path, options = {}) {
  const method = options.method || 'GET';
  const headers = { 'X-OCP-Token': token, ...(options.headers || {}) };
  if (method !== 'GET') headers['Content-Type'] = 'application/json';
  const response = await fetch(path, { ...options, method, headers });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) { const error = new Error(payload.error || `Request failed (${response.status})`); error.status = response.status; throw error; }
  return payload;
}
const copy = value => JSON.parse(JSON.stringify(value));
function profileDraft(profile) { return { extends: profile.extends || '', model: profile.directModel || '', agents: copy(profile.directAgents || []) }; }
function sameDraft(a, b) { return JSON.stringify(a) === JSON.stringify(b); }
function currentProfile() { return state.profiles.find(p => p.name === view.name); }

async function refresh(preferred) {
	clearTimeout(previewTimer);
	previewRequest++;
  state = await api('/api/state');
  if (preferred) view = preferred;
  if (view.type === 'profile' && !state.profiles.some(p => p.name === view.name)) {
    const initial = state.profiles.find(p => p.name === state.active) || state.profiles.find(p => p.name === 'default') || state.profiles[0];
    view = initial ? { type: 'profile', name: initial.name } : { type: 'agents', name: state.agentFiles[0]?.name || '' };
  }
  if (view.type === 'agents' && view.name && !state.agentFiles.some(a => a.name === view.name)) view.name = state.agentFiles[0]?.name || '';
  if (view.type === 'profile') { baseline = profileDraft(currentProfile()); draft = copy(baseline); preview = currentProfile(); }
  dirty = false; render();
}

function render() {
  app.setAttribute('aria-busy', 'false');
  document.querySelector('#agent-count').textContent = state.agentFiles.length;
  document.querySelector('#status').textContent = state.active ? `Active: ${state.active}` : 'No active profile';
  document.querySelector('#apply').disabled = false;
  document.querySelector('#new-profile').disabled = !state.editable;
  profileList.innerHTML = state.profiles.map(p => `<button class="profile-link ${view.type === 'profile' && view.name === p.name ? 'active' : ''}" data-profile="${escapeAttr(p.name)}"><span>${escapeHTML(p.name)}</span>${state.active === p.name ? '<span class="active-dot" title="Active"></span>' : ''}</button>`).join('');
  agentsNav.classList.toggle('active', view.type === 'agents');
  if (view.type === 'profile') renderProfile(); else renderAgents();
}

function modelDefault(profile, agent) {
  return agent?.model || profile.model || 'Default';
}
function modelPicker(id, value, fallback) {
  return `<div class="combobox"><input id="${id}" data-model-picker value="${escapeAttr(value)}" placeholder="${escapeAttr(fallback)}" role="combobox" aria-haspopup="dialog" aria-expanded="false" aria-controls="${id}-list" autocomplete="off" readonly><button type="button" class="model-clear" aria-label="Clear model selection" title="Use default" ${value ? '' : 'hidden'}>&times;</button><span class="model-chevron" aria-hidden="true"></span><div id="${id}-list" class="combobox-popover" role="dialog" aria-label="Choose a model" hidden></div></div>`;
}
function overrideDot(value) { return value ? '<span class="override-dot" title="Overridden in this profile" aria-label="Overridden in this profile"></span>' : ''; }
function effectiveAgent(name) { return (preview?.agents || []).find(a => a.name === name); }

function renderProfile() {
  const profile = currentProfile(); if (!profile) return;
  const effective = preview || profile;
  const direct = new Map(draft.agents.map(a => [a.name, a]));
  const names = new Set([...(effective.agents || []).map(a => a.name), ...direct.keys()]);
	const agentNames = [...names].sort();
  workspace.innerHTML = `<div class="workspace-inner">
    ${state.editable ? '' : `<div class="banner">This source uses inline profiles and is read-only here. ${escapeHTML(state.migrationHint)}</div>`}
    <div class="page-head"><div><div class="eyebrow">Profile ${state.active === profile.name ? '/ active' : ''}</div><h1>${escapeHTML(profile.name)}</h1><div class="inheritance-summary"><span>${draft.extends ? `Inherits from <strong>${escapeHTML(draft.extends)}</strong>` : 'Standalone profile'}</span><button type="button" class="text-button" id="change-parent" ${!state.editable ? 'disabled' : ''}>${draft.extends ? 'Change parent' : 'Add parent'}</button></div></div>
    <div class="head-actions"><button class="button secondary" id="activate" ${state.active === profile.name ? 'disabled' : ''}>${state.active === profile.name ? '<span aria-hidden="true">&#10003;</span> Active' : 'Activate'}</button><button type="button" class="button secondary" id="duplicate-profile" ${!state.editable ? 'disabled' : ''}>Duplicate</button><button type="button" class="button secondary" id="cancel-profile" disabled>Cancel changes</button><button class="button danger" id="delete-profile" ${!state.editable ? 'disabled' : ''}>Delete</button><button class="button" id="save-profile" ${!state.editable ? 'disabled' : ''}>Save profile</button></div></div>
    <section><h2>Profile defaults</h2><div class="card profile-defaults"><div class="field"><label for="profile-model">Model${overrideDot(draft.model)}</label>${modelPicker('profile-model', draft.model, modelDefault(effective))}<div class="field-hint">Effective: ${escapeHTML(effective.model || 'OpenCode default')}${effective.modelOrigin ? ` / ${escapeHTML(effective.modelOrigin)}` : ''}</div></div></div></section>
    <section class="section"><div class="section-head"><h2>Agents</h2><button class="button secondary" id="add-agent" ${!state.editable || !state.agentFiles.length ? 'disabled' : ''}>Add agent</button></div><div class="card agent-table" id="agent-table">${names.size ? agentNames.map((name, index) => agentRow(effectiveAgent(name) || { name, file: '', model: '' }, direct.get(name), effective, index)).join('') : '<div class="empty">No agents in this profile yet.</div>'}</div></section></div>`;
  bindModelPickers();
  workspace.querySelector('#save-profile').addEventListener('click', saveProfile);
  workspace.querySelector('#cancel-profile').addEventListener('click', cancelProfile);
  workspace.querySelector('#activate').addEventListener('click', activateProfile);
  workspace.querySelector('#change-parent').addEventListener('click', changeParent);
  workspace.querySelector('#duplicate-profile').addEventListener('click', duplicateProfile);
  workspace.querySelector('#delete-profile').addEventListener('click', deleteProfile);
  workspace.querySelector('#add-agent').addEventListener('click', addAgentOverride);
  workspace.querySelectorAll('[data-agent-file]').forEach(select => select.addEventListener('change', () => updateAgent(select.closest('.agent-row').dataset.agent, { file: select.value })));
  workspace.querySelectorAll('[data-remove-agent]').forEach(button => button.addEventListener('click', () => { draft.agents = draft.agents.filter(a => a.name !== button.closest('.agent-row').dataset.agent); changed(); }));
  updateControls();
}

function agentRow(agent, direct, profile, index) {
  const selected = direct?.file || '';
  const sourceId = `agent-source-${index}`;
  const modelId = `agent-model-${index}`;
  const files = state.agentFiles.map(file => `<option value="${escapeAttr(file.name)}" ${selected === file.name ? 'selected' : ''}>${escapeHTML(file.name)}</option>`).join('');
  return `<div class="agent-row" data-agent="${escapeAttr(agent.name)}"><div class="agent-name"><strong>${escapeHTML(agent.name)}</strong></div><div class="inline-field"><label for="${sourceId}">Source</label><select id="${sourceId}" data-agent-file><option value="">Inherited source · ${escapeHTML(agent.file || 'none')}</option>${files}</select></div><div class="inline-field"><label for="${modelId}">Model${overrideDot(direct?.model)}</label>${modelPicker(modelId, direct?.model || '', modelDefault(profile, agent))}</div>${direct ? '<button type="button" class="trash-button" data-remove-agent aria-label="Remove from this profile" title="Remove from this profile"><svg viewBox="0 0 20 20" aria-hidden="true"><path d="M6 6v9m4-9v9m4-9v9M4 4h12m-9-2h6l1 2H6l1-2Zm-1 2 .7 13h6.6L14 4"/></svg></button>' : '<span></span>'}</div>`;
}
function updateAgent(name, patch) {
  const existing = draft.agents.find(a => a.name === name);
  if (existing) Object.assign(existing, patch); else draft.agents.push({ name, file: '', model: '', ...patch });
  const item = draft.agents.find(a => a.name === name);
  if (item && !item.file && !item.model && !baseline.agents.some(a => a.name === name && !a.file && !a.model)) draft.agents = draft.agents.filter(a => a !== item);
  changed(!Object.prototype.hasOwnProperty.call(patch, 'model'));
}
function updateDraft(patch) { Object.assign(draft, patch); changed(!Object.prototype.hasOwnProperty.call(patch, 'model')); }
function changed(renderNow = true) { dirty = !sameDraft(draft, baseline); requestPreview(); if (renderNow) renderProfile(); else updateControls(); }
function updateControls() { const cancel = workspace.querySelector('#cancel-profile'), save = workspace.querySelector('#save-profile'); if (cancel) cancel.disabled = !dirty; if (save) save.disabled = !state.editable || !dirty; }
function requestPreview() {
  clearTimeout(previewTimer);
  const request = ++previewRequest;
  const profileName = view.name;
  const revision = state.revision;
  const body = JSON.stringify({ revision, ...draft });
  previewTimer = setTimeout(async () => { try { const result = await api(`/api/profiles/${encodeURIComponent(profileName)}/preview`, { method: 'POST', body }); if (request !== previewRequest || view.type !== 'profile' || view.name !== profileName || state.revision !== revision) return; preview = result; renderProfile(); } catch (error) { if (request === previewRequest) notify(error.message, true); } }, 250);
}
function cancelProfile() { draft = copy(baseline); preview = currentProfile(); dirty = false; clearTimeout(previewTimer); previewRequest++; renderProfile(); }
function discardCurrentDraft() { dirty = false; clearTimeout(previewTimer); previewRequest++; if (view.type === 'profile') { draft = copy(baseline); preview = currentProfile(); } render(); }

function bindModelPickers() {
  workspace.querySelectorAll('[data-model-picker]').forEach(input => {
    input.classList.toggle('default-active', !input.value);
    input.addEventListener('focus', () => { if (input.dataset.suppressOpen) { delete input.dataset.suppressOpen; return; } openModels(input, true); });
    input.addEventListener('click', () => { if (input.getAttribute('aria-expanded') !== 'true') openModels(input, true); });
    input.addEventListener('keydown', event => {
      if (event.key === 'Escape') closeModels(input);
      if (event.key === 'Enter' || event.key === 'ArrowDown') { event.preventDefault(); openModels(input, true); }
    });
    input.closest('.combobox').querySelector('.model-clear').addEventListener('click', event => { event.stopPropagation(); input.value = ''; syncModelPicker(input); modelChanged(input, input.closest('.agent-row')); closeModels(input); });
  });
}
async function openModels(input, focusSearch = false) {
  const list = document.querySelector(`#${cssEscape(input.getAttribute('aria-controls'))}`); if (!list) return;
  input.setAttribute('aria-expanded', 'true'); list.hidden = false;
  list.innerHTML = '<div class="model-loading">Loading models...</div>';
  try {
    modelsPromise ||= api('/api/models').finally(() => { modelsPromise = null; });
    models = await modelsPromise;
  } catch (error) { notify(error.message, true); models = []; }
  if (input.getAttribute('aria-expanded') !== 'true') return;
  const currentProvider = input.value.includes('/') ? input.value.split('/')[0] : '';
  const providers = new Set((models || []).map(model => model.provider));
  if (!input.dataset.provider) input.dataset.provider = providers.has(currentProvider) ? currentProvider : 'all';
  input.dataset.activeModel = input.value;
  renderModelBrowser(input, '');
  positionModelPopover(input, list);
  if (focusSearch) list.querySelector('[data-model-search]')?.focus();
}
function renderModelBrowser(input, query) {
  const popover = document.querySelector(`#${cssEscape(input.getAttribute('aria-controls'))}`); if (!popover) return;
  const counts = {};
  (models || []).forEach(model => { counts[model.provider] = (counts[model.provider] || 0) + 1; });
  const providers = Object.keys(counts).sort((a, b) => a.localeCompare(b));
  const selectedProvider = providers.includes(input.dataset.provider) ? input.dataset.provider : 'all';
  const term = query.trim().toLowerCase();
  const visible = (models || []).filter(model => (selectedProvider === 'all' || model.provider === selectedProvider) && (!term || `${model.provider} ${model.id}`.toLowerCase().includes(term)));
  const custom = query.trim();
  const canUseCustom = custom.includes('/') && !/\s/.test(custom) && !(models || []).some(model => model.id === custom);
  const providerTitle = selectedProvider === 'all' ? 'All models' : selectedProvider;
  const values = [...visible.map(model => model.id), ...(canUseCustom ? [custom] : [])];
  if (term && !visible.some(model => model.id === input.dataset.activeModel) && !(canUseCustom && input.dataset.activeModel === custom)) input.dataset.activeModel = visible[0]?.id || (canUseCustom ? custom : '');
  if (!term && !values.includes(input.dataset.activeModel)) input.dataset.activeModel = values.includes(input.value) ? input.value : (visible[0]?.id || '');
  const optionID = index => `${popover.id}-option-${index}`;
  popover.innerHTML = `<div class="model-browser"><nav class="model-providers" aria-label="Providers"><button type="button" class="model-provider ${selectedProvider === 'all' ? 'active' : ''}" data-provider="all"><span>All models</span><span>${models?.length || 0}</span></button>${providers.map(provider => `<button type="button" class="model-provider ${selectedProvider === provider ? 'active' : ''}" data-provider="${escapeAttr(provider)}"><span>${escapeHTML(provider)}</span><span>${counts[provider]}</span></button>`).join('')}</nav><div class="model-models"><div class="model-search-wrap"><input class="model-search" data-model-search value="${escapeAttr(query)}" placeholder="Search ${escapeAttr(providerTitle)}" aria-label="Search ${escapeAttr(providerTitle)}"></div><div class="model-results-head"><span>${escapeHTML(providerTitle)}</span><span>${visible.length}</span></div><div id="${popover.id}-options" class="model-options" role="listbox">${visible.map((model, index) => { const label = !term && selectedProvider !== 'all' ? model.id.slice(model.provider.length + 1) : model.id; return `<button id="${optionID(index)}" type="button" class="model-option ${input.value === model.id ? 'selected' : ''} ${input.dataset.activeModel === model.id ? 'keyboard-active' : ''}" role="option" aria-selected="${input.value === model.id}" data-value="${escapeAttr(model.id)}"><span class="model-option-name">${escapeHTML(label)}</span><span class="model-option-meta">${escapeHTML(model.provider)}</span></button>`; }).join('')}${canUseCustom ? `<button id="${optionID(visible.length)}" type="button" class="model-option custom-option ${input.dataset.activeModel === custom ? 'keyboard-active' : ''}" role="option" aria-selected="false" data-value="${escapeAttr(custom)}"><span class="model-option-name">Use &quot;${escapeHTML(custom)}&quot;</span><span class="model-option-meta">Custom model</span></button>` : ''}${!visible.length && !canUseCustom ? '<div class="model-empty">No matching models. Enter provider/model to use a custom value.</div>' : ''}</div></div></div>`;
  const search = popover.querySelector('[data-model-search]');
  const optionButtons = [...popover.querySelectorAll('.model-option[data-value]')];
  const activeButton = optionButtons.find(button => button.dataset.value === input.dataset.activeModel);
  search?.setAttribute('aria-controls', `${popover.id}-options`);
  if (activeButton) search?.setAttribute('aria-activedescendant', activeButton.id);
  search?.addEventListener('input', () => { const position = search.selectionStart; renderModelBrowser(input, search.value); const next = popover.querySelector('[data-model-search]'); next?.focus(); next?.setSelectionRange(position, position); });
  search?.addEventListener('keydown', event => {
    if (event.key === 'Escape') { event.preventDefault(); closeModels(input); input.dataset.suppressOpen = 'true'; input.focus(); }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); const current = optionButtons.findIndex(button => button.dataset.value === input.dataset.activeModel); const next = Math.max(0, Math.min(optionButtons.length - 1, current + (event.key === 'ArrowDown' ? 1 : -1))); setActiveModel(input, search, optionButtons, next); }
    if (event.key === 'Enter' && activeButton) { event.preventDefault(); chooseModel(input, input.dataset.activeModel); }
  });
  popover.querySelectorAll('[data-provider]').forEach(button => button.addEventListener('click', () => { input.dataset.provider = button.dataset.provider; input.dataset.activeModel = input.value; renderModelBrowser(input, ''); popover.querySelector('[data-model-search]')?.focus(); }));
  popover.querySelectorAll('.model-option[data-value]').forEach(button => button.addEventListener('click', () => chooseModel(input, button.dataset.value)));
}
function setActiveModel(input, search, buttons, index) { buttons.forEach(button => button.classList.remove('keyboard-active')); const active = buttons[index]; if (!active) return; active.classList.add('keyboard-active'); input.dataset.activeModel = active.dataset.value; search.setAttribute('aria-activedescendant', active.id); active.scrollIntoView({ block: 'nearest' }); }
function positionModelPopover(input, popover) { popover.classList.remove('open-up'); popover.style.removeProperty('--picker-height'); if (window.innerWidth <= 760) return; const rect = input.getBoundingClientRect(); const below = window.innerHeight - rect.bottom - 12; const above = rect.top - 12; const openUp = below < 470 && above > below; popover.classList.toggle('open-up', openUp); popover.style.setProperty('--picker-height', `${Math.min(470, Math.max(280, openUp ? above : below))}px`); }
function chooseModel(input, value) { input.value = value; syncModelPicker(input); modelChanged(input, input.closest('.agent-row')); closeModels(input); input.dataset.suppressOpen = 'true'; input.focus(); }
function syncModelPicker(input) { input.classList.toggle('default-active', !input.value); input.closest('.combobox').querySelector('.model-clear').hidden = !input.value; }
function closeModels(input) { const list = document.querySelector(`#${cssEscape(input.getAttribute('aria-controls'))}`); if (list) list.hidden = true; input.setAttribute('aria-expanded', 'false'); }
function modelChanged(input, row) { input.classList.toggle('default-active', !input.value); if (row) updateAgent(row.dataset.agent, { model: input.value.trim() }); else updateDraft({ model: input.value.trim() }); }
document.addEventListener('pointerdown', event => { document.querySelectorAll('[data-model-picker][aria-expanded="true"]').forEach(input => { if (!input.closest('.combobox').contains(event.target)) closeModels(input); }); });

async function saveProfile() { if (!dirty) return; try { const snapshot = await api(`/api/profiles/${encodeURIComponent(view.name)}`, { method: 'PUT', body: JSON.stringify({ revision: state.revision, ...draft }) }); previewRequest++; state = { ...snapshot, active: state.active }; baseline = profileDraft(currentProfile()); draft = copy(baseline); preview = currentProfile(); dirty = false; render(); notify('Profile saved'); } catch (error) { handle(error); } }
async function activateProfile() { try { state = await api(`/api/activate/${encodeURIComponent(view.name)}`, { method: 'POST', body: '{}' }); render(); notify(`${view.name} is active`); } catch (error) { handle(error); } }
async function deleteProfile() { if (!await confirmDialog(`Permanently delete profile "${view.name}"?`, 'This cannot be undone.', 'Keep profile', 'Delete profile', true)) return; try { const snapshot = await api(`/api/profiles/${encodeURIComponent(view.name)}`, { method: 'DELETE', body: JSON.stringify({ revision: state.revision }) }); state = { ...snapshot, active: state.active }; view.name = ''; await refresh(); notify('Profile deleted'); } catch (error) { handle(error); } }

function changeParent() {
  const profile = currentProfile();
  const available = state.profiles.filter(candidate => candidate.name !== profile.name && !(candidate.chain || []).includes(profile.name));
  const options = [`<option value="" ${draft.extends ? '' : 'selected'}>No parent (standalone)</option>`, ...available.map(candidate => `<option value="${escapeAttr(candidate.name)}" ${draft.extends === candidate.name ? 'selected' : ''}>${escapeHTML(candidate.name)}</option>`)].join('');
  showDialog('Change parent', `<p class="dialog-copy">Explicit model and agent settings in <strong>${escapeHTML(profile.name)}</strong> will stay. Inherited settings will be recalculated from the new parent.</p><div class="field"><label for="parent-profile">Parent profile</label><select id="parent-profile" name="extends">${options}</select><div class="field-hint">You can review the effective result before saving.</div></div>`, values => { updateDraft({ extends: values.extends }); notify('Parent change ready to review'); }, undefined, 'Preview change');
}

async function duplicateProfile() {
  const profile = currentProfile();
  if (dirty && !await confirmDialog('Discard unsaved changes?', 'Duplication uses the saved profile. Your current edits will be lost.', 'Keep editing', 'Discard changes')) return;
  if (dirty) discardCurrentDraft();
  const relationship = profile.extends ? ` The copy will also inherit from ${escapeHTML(profile.extends)}.` : ' The copy will remain standalone.';
  showDialog(`Duplicate ${profile.name}`, `<p class="dialog-copy">Copies this profile's explicit settings.${relationship} Future explicit edits are independent.</p><div class="field"><label for="duplicate-profile-name">Name</label><input id="duplicate-profile-name" name="name" required value="${escapeAttr(profile.name)}-copy"></div>`, async values => { const snapshot = await api('/api/profiles', { method: 'POST', body: JSON.stringify({ revision: state.revision, name: values.name, duplicate: profile.name }) }); showCreatedProfile(snapshot, values.name); notify('Profile duplicated'); }, undefined, 'Duplicate');
}

function showCreatedProfile(snapshot, name) { state = { ...snapshot, active: state.active }; view = { type: 'profile', name }; baseline = profileDraft(currentProfile()); draft = copy(baseline); preview = currentProfile(); dirty = false; render(); }

function addAgentOverride() {
  const assigned = new Set((preview?.agents || []).map(a => a.name));
  const available = state.agentFiles.filter(a => !assigned.has(logicalName(a.name)));
  showDialog('Add agent to profile', `${available.length ? `<div class="field"><label>Agent</label><select name="file">${available.map(a => `<option value="${escapeAttr(a.name)}">${escapeHTML(a.name)}</option>`).join('')}</select></div><details><summary>Advanced</summary><div class="field"><label>Custom logical name</label><input name="name" placeholder="Optional alias"></div></details>` : '<p class="dialog-copy">Every reusable agent is already assigned to this profile.</p>'}<p class="dialog-copy"><a href="#" id="create-agent-link">Create new agent</a></p>`, values => {
    if (!values.file) throw new Error('Select an agent first');
    const name = (values.name || '').trim() || logicalName(values.file);
    if (draft.agents.some(a => a.name === name) || (preview?.agents || []).some(a => a.name === name)) throw new Error(`Agent "${name}" is already assigned`);
    draft.agents.push({ name, file: values.file, model: '' }); changed();
  }, form => form.querySelector('#create-agent-link')?.addEventListener('click', async event => { event.preventDefault(); dialog.close(); if (await navigate({ type: 'agents', name: '' })) newAgent(); }));
}
function logicalName(file) { return String(file).replace(/\.md$/i, ''); }

function renderAgents() {
  const agent = state.agentFiles.find(a => a.name === view.name) || state.agentFiles[0]; if (agent && !view.name) view.name = agent.name;
  workspace.innerHTML = `<div class="workspace-inner">${state.editable ? '' : `<div class="banner">Agent editing is disabled until profiles are migrated. ${escapeHTML(state.migrationHint)}</div>`}<div class="page-head"><div><div class="eyebrow">Reusable sources</div><h1>Agents</h1><p class="subtitle">Markdown files shared across profile assignments.</p></div><div class="head-actions"><button class="button" id="new-agent" ${!state.editable ? 'disabled' : ''}>New agent</button></div></div><div class="card library"><nav class="library-list">${state.agentFiles.map(a => `<button class="library-item ${agent?.name === a.name ? 'active' : ''}" data-agent-file="${escapeAttr(a.name)}">${escapeHTML(a.name)}</button>`).join('') || '<div class="empty">No agent files</div>'}</nav><div class="editor">${agent ? `<div class="editor-meta"><div><strong>${escapeHTML(agent.name)}</strong><div class="usage">${agent.usage.length ? `Used by ${agent.usage.map(escapeHTML).join(', ')}` : 'Not assigned to a profile'}</div></div><div><button class="text-button danger" id="delete-agent" ${!state.editable ? 'disabled' : ''}>Delete</button> <button type="button" class="button secondary" id="cancel-agent" disabled>Cancel changes</button> <button class="button" id="save-agent" disabled>Save</button></div></div><textarea id="agent-content" spellcheck="false">${escapeHTML(agent.content)}</textarea>` : '<div class="empty">Create an agent source to begin.</div>'}</div></div></div>`;
  workspace.querySelector('#new-agent')?.addEventListener('click', newAgent); workspace.querySelector('#save-agent')?.addEventListener('click', saveAgent); workspace.querySelector('#cancel-agent')?.addEventListener('click', () => { dirty = false; renderAgents(); }); workspace.querySelector('#delete-agent')?.addEventListener('click', deleteAgent); bindDirty(workspace.querySelectorAll('textarea'));
}
async function saveAgent() { try { const snapshot = await api(`/api/agents/${encodeURIComponent(view.name)}`, { method: 'PUT', body: JSON.stringify({ revision: state.revision, content: workspace.querySelector('#agent-content').value }) }); state = { ...snapshot, active: state.active }; dirty = false; render(); notify('Agent saved'); } catch (error) { handle(error); } }
async function newAgent() { if (dirty && !await confirmDialog('Discard unsaved changes?', 'Your edits will be lost.', 'Keep editing', 'Discard changes')) return; if (dirty) discardCurrentDraft(); showDialog('New agent', '<div class="field"><label>Filename</label><input name="name" required placeholder="reviewer.md"></div>', async values => { const name = values.name.endsWith('.md') ? values.name : `${values.name}.md`; const snapshot = await api('/api/agents', { method: 'POST', body: JSON.stringify({ revision: state.revision, name, content: '---\ndescription: Describe this agent\nmode: subagent\n---\n\nWrite agent instructions here.\n' }) }); state = { ...snapshot, active: state.active }; view = { type: 'agents', name }; dirty = false; render(); notify('Agent created'); }); }
async function deleteAgent() { if (!await confirmDialog(`Delete agent "${view.name}"?`, 'Profiles using it will need another source.', 'Keep agent', 'Delete agent', true)) return; try { const snapshot = await api(`/api/agents/${encodeURIComponent(view.name)}`, { method: 'DELETE', body: JSON.stringify({ revision: state.revision }) }); state = { ...snapshot, active: state.active }; view.name = state.agentFiles[0]?.name || ''; dirty = false; render(); notify('Agent deleted'); } catch (error) { handle(error); } }

async function newProfile() { if (dirty && !await confirmDialog('Discard unsaved changes?', 'Your edits will be lost.', 'Keep editing', 'Discard changes')) return; if (dirty) discardCurrentDraft(); showDialog('New profile', `<p class="dialog-copy">Start standalone or inherit settings from an existing profile.</p><div class="field"><label for="new-profile-name">Name</label><input id="new-profile-name" name="name" required placeholder="research"></div><div class="field"><label for="new-profile-parent">Starting point</label><select id="new-profile-parent" name="extends"><option value="">Standalone (no parent)</option><optgroup label="Inherit from">${state.profiles.map(p => `<option value="${escapeAttr(p.name)}">${escapeHTML(p.name)}</option>`).join('')}</optgroup></select><div class="field-hint">Inherited settings stay linked to the parent profile.</div></div>`, async values => { const snapshot = await api('/api/profiles', { method: 'POST', body: JSON.stringify({ revision: state.revision, name: values.name, extends: values.extends }) }); showCreatedProfile(snapshot, values.name); notify('Profile created'); }, undefined, 'Create profile'); }
function showDialog(title, fields, submit, afterRender, submitLabel = 'Continue') { dialog.oncancel = null; dialogForm.innerHTML = `<div class="dialog-body"><h2 class="dialog-title">${escapeHTML(title)}</h2><div class="dialog-fields">${fields}</div><div class="dialog-actions"><button type="button" class="button secondary" data-cancel>Cancel</button><button class="button" value="default">${escapeHTML(submitLabel)}</button></div></div>`; dialogForm.querySelector('[data-cancel]').onclick = () => dialog.close(); dialogForm.onsubmit = async event => { event.preventDefault(); try { await submit(Object.fromEntries(new FormData(dialogForm))); dialog.close(); } catch (error) { notify(error.message, true); } }; dialog.showModal(); afterRender?.(dialogForm); (dialogForm.querySelector('input') || dialogForm.querySelector('select'))?.focus(); }
function confirmDialog(title, body, keep, proceed, danger) { return new Promise(resolve => { let settled = false; const finish = value => { if (settled) return; settled = true; dialog.close(); resolve(value); }; dialogForm.innerHTML = `<div class="dialog-body"><h2 class="dialog-title">${escapeHTML(title)}</h2><p class="dialog-copy">${escapeHTML(body)}</p><div class="dialog-actions"><button type="button" class="button secondary" data-keep>${escapeHTML(keep)}</button><button type="button" class="button ${danger ? 'danger' : ''}" data-proceed>${escapeHTML(proceed)}</button></div></div>`; dialogForm.querySelector('[data-keep]').onclick = () => finish(false); dialogForm.querySelector('[data-proceed]').onclick = () => finish(true); dialog.oncancel = event => { event.preventDefault(); finish(false); }; dialog.showModal(); }); }
function bindDirty(elements) { elements.forEach(e => e.addEventListener('input', () => { dirty = true; workspace.querySelector('#cancel-agent')?.removeAttribute('disabled'); workspace.querySelector('#save-agent')?.removeAttribute('disabled'); })); }
async function navigate(next) { if (dirty && !await confirmDialog('Leave without saving changes?', 'Your edits will be discarded.', 'Keep editing', 'Discard changes')) return false; dirty = false; clearTimeout(previewTimer); previewRequest++; view = next; sidebar.classList.remove('open'); if (view.type === 'profile') { baseline = profileDraft(currentProfile()); draft = copy(baseline); preview = currentProfile(); } render(); return true; }
function notify(message, error = false) { const toast = document.querySelector('#toast'); toast.textContent = message; toast.className = `toast show${error ? ' error' : ''}`; clearTimeout(toastTimer); toastTimer = setTimeout(() => { toast.className = 'toast'; }, 3200); }
async function handle(error) { notify(error.message, true); if (error.status === 409 && await confirmDialog('Changes were made elsewhere.', 'Reload to see the latest saved configuration.', 'Keep editing', 'Reload')) await refresh(view); }
function escapeHTML(value = '') { return String(value).replace(/[&<>'"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' })[c]); }
const escapeAttr = escapeHTML; const cssEscape = value => window.CSS?.escape ? CSS.escape(value) : value.replace(/[^a-zA-Z0-9_-]/g, '\\$&');
profileList.addEventListener('click', e => { const b = e.target.closest('[data-profile]'); if (b) navigate({ type: 'profile', name: b.dataset.profile }); }); agentsNav.addEventListener('click', () => navigate({ type: 'agents', name: state.agentFiles[0]?.name || '' })); workspace.addEventListener('click', e => { const b = e.target.closest('[data-agent-file]'); if (b?.classList.contains('library-item')) navigate({ type: 'agents', name: b.dataset.agentFile }); }); document.querySelector('#new-profile').addEventListener('click', newProfile); document.querySelector('#mobile-nav').addEventListener('click', () => sidebar.classList.toggle('open')); document.querySelector('#apply').addEventListener('click', async () => { if (dirty && !await confirmDialog('Apply saved profiles?', 'Your unsaved edits will be discarded.', 'Keep editing', 'Apply saved profiles')) return; try { const result = await api('/api/apply', { method: 'POST', body: '{}' }); await refresh(view); notify(`Applied ${result.profiles.length} profile${result.profiles.length === 1 ? '' : 's'}`); } catch (error) { handle(error); } }); window.addEventListener('beforeunload', e => { if (dirty) { e.preventDefault(); e.returnValue = ''; } }); refresh().catch(handle);
