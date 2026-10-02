<script lang="ts">
  /**
   * Issue tracker connections, and which one the active project reads from.
   *
   * Two halves: the connections are global (a Redmine instance and its API
   * key), the binding belongs to the project that is open. A project bound to
   * a connection shows that tracker's issues in its Tasks view instead of the
   * built-in list.
   */
  import { claimKeyForDialog } from '../../utils/dialogKeys';
  import * as App from '../../../../wailsjs/go/main/App';
  import type { main, tracker } from '../../../../wailsjs/go/models';
  import Select from '../common/Select.svelte';
  import ConfirmDialog from './ConfirmDialog.svelte';
  import { t } from '../../i18n';
  import { autoFocusDialog } from '../../utils/dialogActions';
  import DialogCloseButton from '../common/DialogCloseButton.svelte';
  import { describeBackendError } from '../../utils/backendError';
  import { activeProjectId } from '../../stores/projects';
  import { refreshTrackerBinding } from '../../stores/tracker';
  import { selectedSession } from '../../stores/sessions';

  export let show = false;

  type Connection = main.TrackerConnectionInfo;

  let connections: Connection[] = [];
  let loading = false;
  let error = '';
  let saving = false;

  // Editor state. `editingId === ''` means "new connection".
  let editing = false;
  let editingId = '';
  let fName = '';
  let fKind = 'redmine';
  let fBaseUrl = '';
  let fToken = '';
  let hasStoredToken = false;

  let testing = false;
  let testResult = '';
  let testError = '';

  let deleteTarget: Connection | null = null;

  // The active project's binding.
  let bindConnectionId = '';
  let bindScope = '';
  let bindingSaved = false;

  // The selected session's own binding. An empty connection means the
  // session follows the project.
  let sessionConnectionId = '';
  let sessionScope = '';
  let sessionSaved = false;

  // Tracker projects per connection, fetched when a connection is picked so
  // the scope can be chosen from a list rather than typed as an ID.
  let scopesByConnection: Record<string, tracker.Scope[]> = {};
  let scopeErrors: Record<string, string> = {};
  let scopesLoading: Record<string, boolean> = {};

  let wasShown = false;
  $: if (show && !wasShown) {
    wasShown = true;
    void open();
  } else if (!show && wasShown) {
    wasShown = false;
  }

  async function open() {
    editing = false;
    error = '';
    bindingSaved = false;
    sessionSaved = false;
    scopesByConnection = {};
    scopeErrors = {};
    await load();
    try {
      const binding = await App.GetTrackerBinding($activeProjectId, '');
      bindConnectionId = binding?.connectionId || '';
      bindScope = binding?.scope || '';
      const own = $selectedSession
        ? await App.GetTrackerBinding($activeProjectId, $selectedSession.id)
        : null;
      const isOwn = own?.source === 'session';
      sessionConnectionId = isOwn ? own!.connectionId : '';
      sessionScope = isOwn ? own!.scope : '';
    } catch (e) {
      error = describeBackendError(e);
    }
  }

  async function load() {
    loading = true;
    try {
      connections = (await App.GetTrackerConnections()) || [];
    } catch (e) {
      error = describeBackendError(e);
    } finally {
      loading = false;
    }
  }

  function close() {
    show = false;
  }

  function startNew() {
    editing = true;
    editingId = '';
    fName = '';
    fKind = 'redmine';
    fBaseUrl = '';
    fToken = '';
    hasStoredToken = false;
    testResult = '';
    testError = '';
  }

  function startEdit(conn: Connection) {
    editing = true;
    editingId = conn.id;
    fName = conn.name;
    fKind = conn.kind || 'redmine';
    fBaseUrl = conn.baseUrl;
    fToken = '';
    hasStoredToken = conn.hasToken;
    testResult = '';
    testError = '';
  }

  function request(): main.TrackerConnectionRequest {
    return { id: editingId, name: fName, kind: fKind, baseUrl: fBaseUrl, token: fToken } as main.TrackerConnectionRequest;
  }

  async function test() {
    testing = true;
    testResult = '';
    testError = '';
    try {
      const user = await App.TestTrackerConnection(request());
      testResult = $t('trackers.testOk').replace('{user}', user);
    } catch (e) {
      testError = describeBackendError(e);
    } finally {
      testing = false;
    }
  }

  async function save() {
    saving = true;
    error = '';
    try {
      connections = (await App.SaveTrackerConnection(request())) || [];
      editing = false;
      await refreshTrackerBinding();
    } catch (e) {
      error = describeBackendError(e);
    } finally {
      saving = false;
    }
  }

  async function confirmDelete() {
    const target = deleteTarget;
    deleteTarget = null;
    if (!target) return;
    try {
      connections = (await App.DeleteTrackerConnection(target.id)) || [];
      if (bindConnectionId === target.id) {
        bindConnectionId = '';
        bindScope = '';
      }
      if (sessionConnectionId === target.id) {
        sessionConnectionId = '';
        sessionScope = '';
      }
      await refreshTrackerBinding();
    } catch (e) {
      error = describeBackendError(e);
    }
  }

  $: if (show && bindConnectionId) ensureScopes(bindConnectionId);
  $: if (show && sessionConnectionId) ensureScopes(sessionConnectionId);

  function ensureScopes(connectionId: string) {
    if (connectionId in scopesByConnection || scopesLoading[connectionId]) return;
    scopesLoading = { ...scopesLoading, [connectionId]: true };
    App.TrackerListScopes(connectionId)
      .then((result) => {
        scopesByConnection = { ...scopesByConnection, [connectionId]: result || [] };
      })
      .catch((e) => {
        scopesByConnection = { ...scopesByConnection, [connectionId]: [] };
        scopeErrors = { ...scopeErrors, [connectionId]: describeBackendError(e) };
      })
      .finally(() => {
        scopesLoading = { ...scopesLoading, [connectionId]: false };
      });
  }

  function scopeName(connectionId: string, scope: string): string {
    return (scopesByConnection[connectionId] || []).find(s => s.id === scope)?.name || '';
  }

  function scopeOptionsFor(connectionId: string, scopes: Record<string, tracker.Scope[]>) {
    return [
      { value: '', label: $t('trackers.allAssigned') },
      ...(scopes[connectionId] || []).map(s => ({ value: s.id, label: s.parent ? `${s.parent} › ${s.name}` : s.name })),
    ];
  }

  async function saveBinding() {
    error = '';
    bindingSaved = false;
    const scope = bindConnectionId ? bindScope : '';
    try {
      await App.SetTrackerBinding(bindConnectionId, scope, scopeName(bindConnectionId, scope), $activeProjectId);
      await refreshTrackerBinding();
      bindingSaved = true;
    } catch (e) {
      error = describeBackendError(e);
    }
  }

  async function saveSessionBinding() {
    const session = $selectedSession;
    if (!session) return;
    error = '';
    sessionSaved = false;
    const scope = sessionConnectionId ? sessionScope : '';
    try {
      await App.SetSessionTrackerBinding(session.id, sessionConnectionId, scope, scopeName(sessionConnectionId, scope), $activeProjectId);
      await refreshTrackerBinding();
      sessionSaved = true;
    } catch (e) {
      error = describeBackendError(e);
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      claimKeyForDialog();
      e.stopPropagation();
      if (deleteTarget) {
        deleteTarget = null;
      } else if (editing) {
        editing = false;
      } else {
        close();
      }
    }
  }

  function focusInput(node: HTMLInputElement) {
    node.focus();
  }

  $: kindOptions = [{ value: 'redmine', label: 'Redmine' }];

  $: bindOptions = [
    { value: '', label: $t('trackers.builtInTasks') },
    ...connections.map(c => ({ value: c.id, label: c.name || c.baseUrl })),
  ];

  $: sessionOptions = [
    { value: '', label: $t('trackers.followProject') },
    ...connections.map(c => ({ value: c.id, label: c.name || c.baseUrl })),
  ];

  $: projectScopeOptions = scopeOptionsFor(bindConnectionId, scopesByConnection);
  $: sessionScopeOptions = scopeOptionsFor(sessionConnectionId, scopesByConnection);
</script>

{#if show}
  <!-- svelte-ignore a11y-no-noninteractive-element-interactions -->
  <div
    class="dialog-overlay manager-overlay"
    use:autoFocusDialog
    tabindex="-1"
    role="dialog"
    aria-modal="true"
    on:keydown={handleKeydown}
  >
    <div class="dialog-content manager">
      <div class="dialog-header">
        <h2>{$t('trackers.managerTitle')}</h2>
        <DialogCloseButton on:click={close} />
      </div>

      <div class="dialog-body">
        {#if error}<div class="error-line">{error}</div>{/if}

        {#if editing}
          <div class="form">
            <h3 class="form-title">
              {editingId ? $t('trackers.editConnection') : $t('trackers.newConnection')}
            </h3>

            <label class="field">
              <span class="field-label">{$t('trackers.fieldName')}</span>
              <input use:focusInput bind:value={fName} placeholder="Redmine" />
            </label>

            <label class="field">
              <span class="field-label">{$t('trackers.fieldKind')}</span>
              <Select bind:value={fKind} options={kindOptions} />
            </label>

            <label class="field">
              <span class="field-label">{$t('trackers.fieldUrl')}</span>
              <input bind:value={fBaseUrl} placeholder="https://redmine.example.com" />
            </label>

            <label class="field">
              <span class="field-label">{$t('trackers.fieldToken')}</span>
              <input
                type="password"
                bind:value={fToken}
                placeholder={hasStoredToken ? $t('trackers.tokenStored') : ''}
                autocomplete="off"
              />
              <span class="hint">{$t('trackers.tokenHint')}</span>
            </label>

            {#if testResult}<p class="hint ok">{testResult}</p>{/if}
            {#if testError}<p class="hint warn">{testError}</p>{/if}

            <div class="form-actions">
              <button class="btn-secondary" on:click={test} disabled={testing || !fBaseUrl.trim()}>
                {testing ? $t('trackers.testing') : $t('trackers.test')}
              </button>
              <span class="spacer"></span>
              <button class="btn-secondary" on:click={() => (editing = false)}>{$t('common.cancel')}</button>
              <button class="btn-primary" on:click={save} disabled={saving}>
                {saving ? $t('common.saving') : $t('common.save')}
              </button>
            </div>
          </div>
        {:else}
          <section class="block">
            <div class="block-head">
              <h3>{$t('trackers.connections')}</h3>
              <button class="btn-primary small" on:click={startNew}>{$t('trackers.add')}</button>
            </div>

            {#if loading}
              <p class="empty">{$t('common.loading')}</p>
            {:else if connections.length === 0}
              <p class="empty">{$t('trackers.empty')}</p>
            {:else}
              <ul class="list">
                {#each connections as conn (conn.id)}
                  <li class="row">
                    <div class="row-main">
                      <div class="row-name">
                        {conn.name || conn.baseUrl}
                        {#if !conn.hasToken}<span class="badge warn">{$t('trackers.noToken')}</span>{/if}
                      </div>
                      <div class="row-detail">{conn.kind === 'redmine' ? 'Redmine' : conn.kind} · {conn.baseUrl}</div>
                    </div>
                    <div class="row-actions">
                      <button class="btn-secondary small" on:click={() => startEdit(conn)}>{$t('common.edit')}</button>
                      <button class="btn-secondary small danger" on:click={() => (deleteTarget = conn)}>{$t('common.delete')}</button>
                    </div>
                  </li>
                {/each}
              </ul>
            {/if}
          </section>

          <section class="block">
            <div class="block-head">
              <h3>{$t('trackers.thisProject')}</h3>
            </div>
            <p class="hint">{$t('trackers.thisProjectHint')}</p>

            <div class="form">
              <label class="field">
                <span class="field-label">{$t('trackers.source')}</span>
                <Select bind:value={bindConnectionId} options={bindOptions} on:change={() => { bindScope = ''; bindingSaved = false; }} />
              </label>

              {#if bindConnectionId}
                <label class="field">
                  <span class="field-label">{$t('trackers.scope')}</span>
                  <Select searchable bind:value={bindScope} options={projectScopeOptions} on:change={() => (bindingSaved = false)} />
                  {#if scopesLoading[bindConnectionId]}<span class="hint">{$t('common.loading')}</span>{/if}
                  {#if scopeErrors[bindConnectionId]}<span class="hint warn">{scopeErrors[bindConnectionId]}</span>{/if}
                </label>
              {/if}

              <div class="form-actions">
                {#if bindingSaved}<span class="hint ok">{$t('trackers.bindingSaved')}</span>{/if}
                <button class="btn-primary" on:click={saveBinding}>{$t('common.save')}</button>
              </div>
            </div>
          </section>

          {#if $selectedSession}
            <section class="block">
              <div class="block-head">
                <h3>{$t('trackers.thisSession').replace('{name}', $selectedSession.name)}</h3>
              </div>
              <p class="hint">{$t('trackers.thisSessionHint')}</p>

              <div class="form">
                <label class="field">
                  <span class="field-label">{$t('trackers.source')}</span>
                  <Select bind:value={sessionConnectionId} options={sessionOptions} on:change={() => { sessionScope = ''; sessionSaved = false; }} />
                </label>

                {#if sessionConnectionId}
                  <label class="field">
                    <span class="field-label">{$t('trackers.scope')}</span>
                    <Select searchable bind:value={sessionScope} options={sessionScopeOptions} on:change={() => (sessionSaved = false)} />
                    {#if scopesLoading[sessionConnectionId]}<span class="hint">{$t('common.loading')}</span>{/if}
                    {#if scopeErrors[sessionConnectionId]}<span class="hint warn">{scopeErrors[sessionConnectionId]}</span>{/if}
                  </label>
                {/if}

                <div class="form-actions">
                  {#if sessionSaved}<span class="hint ok">{$t('trackers.bindingSaved')}</span>{/if}
                  <button class="btn-primary" on:click={saveSessionBinding}>{$t('common.save')}</button>
                </div>
              </div>
            </section>
          {/if}
        {/if}
      </div>
    </div>
  </div>
{/if}

<ConfirmDialog
  show={!!deleteTarget}
  title={$t('trackers.deleteTitle')}
  message={$t('trackers.deleteMessage').replace('{name}', deleteTarget?.name || deleteTarget?.baseUrl || '')}
  confirmText={$t('common.delete')}
  cancelText={$t('common.cancel')}
  on:confirm={confirmDelete}
  on:cancel={() => (deleteTarget = null)}
/>

<style>
  .manager {
    width: min(620px, 92vw);
    max-width: min(620px, 92vw);
    max-height: 82vh;
  }

  .block + .block {
    margin-top: 22px;
  }

  .block-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
  }

  .block-head h3 {
    margin: 0;
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: #6b7280;
  }

  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 11px;
    border-radius: 7px;
    background: rgba(255, 255, 255, 0.03);
    border: 1px solid rgba(255, 255, 255, 0.06);
  }

  .row-main {
    flex: 1;
    min-width: 0;
  }

  .row-name {
    display: flex;
    align-items: center;
    gap: 7px;
    font-size: 13px;
    color: #e4e4e7;
  }

  .row-detail {
    font-size: 11px;
    color: #8b8b93;
    margin-top: 2px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .row-actions {
    display: flex;
    gap: 6px;
  }

  .badge {
    font-size: 10px;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-dark, #3f3f46);
    color: #e4e4e7;
  }

  .badge.warn {
    background: rgba(251, 191, 36, 0.15);
    color: #fbbf24;
  }

  .small {
    padding: 4px 10px;
    font-size: 12px;
  }

  .danger:hover {
    color: #f87171;
  }

  .form {
    display: flex;
    flex-direction: column;
    gap: 11px;
  }

  .form-title {
    margin: 0 0 2px;
    font-size: 13px;
    color: #e4e4e7;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .field-label {
    font-size: 11px;
    color: #a1a1aa;
  }

  .field input {
    background: rgba(0, 0, 0, 0.25);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 6px;
    padding: 7px 9px;
    color: #e4e4e7;
    font-size: 13px;
  }

  .field input:focus {
    outline: none;
    border-color: var(--accent, #6366f1);
  }

  .hint {
    font-size: 11px;
    color: #71717a;
    margin: 0;
  }

  .hint.warn {
    color: #fbbf24;
  }

  .hint.ok {
    color: #4ade80;
  }

  .form-actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 4px;
  }

  .spacer {
    flex: 1;
  }

  .empty {
    text-align: center;
    color: #71717a;
    font-size: 12px;
    padding: 18px 0;
  }

  .error-line {
    background: rgba(248, 113, 113, 0.1);
    border: 1px solid rgba(248, 113, 113, 0.3);
    color: #fca5a5;
    border-radius: 6px;
    padding: 8px 10px;
    font-size: 12px;
    margin-bottom: 11px;
  }
</style>
