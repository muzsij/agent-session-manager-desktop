<script lang="ts">
  /**
   * The open issues assigned to the user in the project's external tracker,
   * shown in the Tasks view instead of the built-in list.
   *
   * Read-only for now: an issue can be read, opened in the tracker, and typed
   * into the session's active tab for its agent. Changing the issue is left to
   * the tracker — or to the agent, when it has access to it.
   */
  import { createEventDispatcher } from 'svelte';
  import * as App from '../../../../wailsjs/go/main/App';
  import type { tracker } from '../../../../wailsjs/go/models';
  import { BrowserOpenURL } from '../../../../wailsjs/runtime/runtime';
  import { t } from '../../i18n';
  import { trackerBinding, refreshTrackerBinding } from '../../stores/tracker';
  import Select from '../common/Select.svelte';
  import { activeProjectId } from '../../stores/projects';
  import { selectedSessionId } from '../../stores/sessions';
  import { describeBackendError } from '../../utils/backendError';

  export let active = false;

  const dispatch = createEventDispatcher<{ taskSent: { taskId: string } }>();

  let issues: tracker.Issue[] = [];
  let loading = false;
  let error = '';
  let query = '';
  let expandedId = '';
  let sendingId = '';
  let loadedFor = '';
  let generation = 0;

  // What the list was loaded for: a different session or binding needs a new
  // list, the same one is kept while the view is switched away and back.
  $: bindingKey = $trackerBinding
    ? `${$activeProjectId}|${$selectedSessionId}|${$trackerBinding.connectionId}|${$trackerBinding.scope}`
    : '';
  $: if (active && bindingKey && bindingKey !== loadedFor) void load();

  async function load() {
    const key = bindingKey;
    const current = ++generation;
    loadedFor = key;
    loading = true;
    error = '';
    try {
      const result = await App.TrackerListIssues($activeProjectId, $selectedSessionId || '');
      if (current !== generation) return;
      issues = result || [];
    } catch (e) {
      if (current !== generation) return;
      issues = [];
      error = describeBackendError(e);
    } finally {
      if (current === generation) loading = false;
    }
  }

  $: needle = query.trim().toLowerCase();
  $: visible = needle
    ? issues.filter(issue =>
        issue.title.toLowerCase().includes(needle) ||
        issue.ref.toLowerCase().includes(needle) ||
        (issue.project || '').toLowerCase().includes(needle))
    : issues;

  async function send(issue: tracker.Issue) {
    const sessionId = $selectedSessionId;
    if (!sessionId || sendingId) return;
    sendingId = issue.id;
    error = '';
    try {
      await App.TrackerSendIssueToAgent(sessionId, issue.id, '', $activeProjectId);
      dispatch('taskSent', { taskId: issue.id });
    } catch (e) {
      error = describeBackendError(e);
    } finally {
      sendingId = '';
    }
  }

  // Which part of the tracker this session reads: the project's choice, or a
  // tracker project of its own. The choice is saved for the session, so each
  // session can follow the Redmine project it works on.
  const PROJECT_DEFAULT = '__project__';
  let scopes: tracker.Scope[] = [];
  let scopesFor = '';
  let scopeSaving = false;

  $: connectionId = $trackerBinding?.connectionId || '';
  $: if (active && connectionId && connectionId !== scopesFor) void loadScopes(connectionId);

  async function loadScopes(id: string) {
    scopesFor = id;
    try {
      const result = (await App.TrackerListScopes(id)) || [];
      if (scopesFor === id) scopes = result;
    } catch (e) {
      // The picker still offers the project default and "everything"; the
      // list itself reports what is wrong with the connection.
      if (scopesFor === id) scopes = [];
      console.error('Failed to load tracker projects:', e);
    }
  }

  $: scopeValue = $trackerBinding?.source === 'session' ? ($trackerBinding?.scope || '') : PROJECT_DEFAULT;
  $: scopeOptions = [
    ...($trackerBinding?.projectBound
      ? [{ value: PROJECT_DEFAULT, label: $t('tracker.projectDefault').replace('{name}', $trackerBinding.projectScopeName || $t('trackers.allAssigned')) }]
      : []),
    { value: '', label: $t('trackers.allAssigned') },
    ...scopes.map(s => ({ value: s.id, label: s.parent ? `${s.parent} › ${s.name}` : s.name })),
  ];

  async function chooseScope(value: string) {
    const sessionId = $selectedSessionId;
    if (!sessionId || !$trackerBinding || value === scopeValue) return;
    scopeSaving = true;
    error = '';
    try {
      if (value === PROJECT_DEFAULT) {
        await App.SetSessionTrackerBinding(sessionId, '', '', '', $activeProjectId);
      } else {
        const name = scopes.find(s => s.id === value)?.name || '';
        await App.SetSessionTrackerBinding(sessionId, $trackerBinding.connectionId, value, name, $activeProjectId);
      }
      await refreshTrackerBinding();
    } catch (e) {
      error = describeBackendError(e);
    } finally {
      scopeSaving = false;
    }
  }

  function open(issue: tracker.Issue) {
    if (issue.url) BrowserOpenURL(issue.url);
  }

  function toggle(issue: tracker.Issue) {
    expandedId = expandedId === issue.id ? '' : issue.id;
  }

  function formatDue(value: unknown): string {
    if (!value) return '';
    const date = new Date(value as string);
    return Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString();
  }

  function isOverdue(value: unknown): boolean {
    if (!value) return false;
    const date = new Date(value as string);
    date.setHours(23, 59, 59, 999);
    return date.getTime() < Date.now();
  }
</script>

<div class="tracker-panel">
  <div class="tracker-header">
    <div class="header-left">
      <span class="tracker-title">{$t('tracker.title')}</span>
      {#if issues.length > 0}<span class="tracker-count">{issues.length}</span>{/if}
      {#if $trackerBinding}
        <span class="tracker-source" title={$trackerBinding.connectionName}>
          {$trackerBinding.connectionName}{#if $trackerBinding.scopeName} · {$trackerBinding.scopeName}{/if}
        </span>
      {/if}
    </div>
    <button class="icon-btn" on:click={load} disabled={loading} title={$t('common.refresh')} aria-label={$t('common.refresh')}>
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class:spinning={loading}>
        <path d="M21 12a9 9 0 11-3-6.7L21 8"/>
        <path d="M21 3v5h-5"/>
      </svg>
    </button>
  </div>

  <div class="action-bar">
    <div class="tracker-search">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="8"/>
        <path d="M21 21l-4.35-4.35"/>
      </svg>
      <input type="text" bind:value={query} placeholder={$t('tracker.filterPlaceholder')} />
    </div>
    {#if $selectedSessionId}
      <div class="scope-picker" title={$t('tracker.scopeHint')}>
        <Select
          small
          searchable
          value={scopeValue}
          options={scopeOptions}
          on:change={(e) => chooseScope(e.detail)}
        />
        {#if scopeSaving}<span class="scope-saving">…</span>{/if}
      </div>
    {/if}
  </div>

  <div class="tracker-body">
    {#if error}<div class="error-line">{error}</div>{/if}

    {#if loading && issues.length === 0}
      <p class="empty">{$t('common.loading')}</p>
    {:else if !error && visible.length === 0}
      <p class="empty">{needle ? $t('tracker.noMatch') : $t('tracker.empty')}</p>
    {:else}
      <ul class="issue-list">
        {#each visible as issue (issue.id)}
          <li class="issue" class:expanded={expandedId === issue.id}>
            <div class="issue-row">
              <button class="issue-main" on:click={() => toggle(issue)}>
                <span class="issue-ref">{issue.ref}</span>
                <span class="issue-title">{issue.title}</span>
              </button>
              <div class="issue-actions">
                <button class="icon-btn" on:click={() => open(issue)} title={$t('tracker.openInTracker')} aria-label={$t('tracker.openInTracker')}>
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6"/>
                    <path d="M15 3h6v6"/>
                    <path d="M10 14L21 3"/>
                  </svg>
                </button>
                <button
                  class="send-btn"
                  on:click={() => send(issue)}
                  disabled={!$selectedSessionId || !!sendingId}
                >{sendingId === issue.id ? $t('tracker.sending') : $t('tasks.sendToAgent')}</button>
              </div>
            </div>
            <div class="issue-meta">
              {#if issue.status?.name}<span class="chip">{issue.status.name}</span>{/if}
              {#if issue.priority}<span class="chip">{issue.priority}</span>{/if}
              {#if issue.tracker}<span>{issue.tracker}</span>{/if}
              {#if issue.project && !$trackerBinding?.scope}<span>{issue.project}</span>{/if}
              {#if issue.dueAt}
                <span class:overdue={isOverdue(issue.dueAt)}>{$t('tracker.due')}: {formatDue(issue.dueAt)}</span>
              {/if}
            </div>
            {#if expandedId === issue.id}
              <div class="issue-description">
                {#if issue.description?.trim()}{issue.description}{:else}<em>{$t('tracker.noDescription')}</em>{/if}
              </div>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>

<style>
  .tracker-panel {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg-surface);
    min-height: 0;
  }

  .tracker-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 16px;
    background: rgba(0, 0, 0, 0.3);
    border-bottom: 1px solid rgba(255, 255, 255, 0.05);
  }

  .header-left {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }

  .tracker-title {
    font-size: 13px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: #6b7280;
  }

  .tracker-count {
    font-size: 12px;
    color: #4ade80;
    background: rgba(74, 222, 128, 0.1);
    padding: 2px 8px;
    border-radius: 10px;
  }

  .tracker-source {
    font-size: 12px;
    color: #8b8b93;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .action-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 16px;
    border-bottom: 1px solid rgba(255, 255, 255, 0.05);
  }

  .scope-picker {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 180px;
    max-width: 320px;
  }

  .scope-saving {
    color: #8b8b93;
    font-size: 12px;
  }

  .tracker-search {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 0 8px;
    background: rgba(0, 0, 0, 0.25);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 6px;
    color: #6b7280;
    flex: 1;
    max-width: 280px;
  }

  .tracker-search:focus-within {
    border-color: rgba(var(--accent-rgb), 0.5);
  }

  .tracker-search input {
    flex: 1;
    min-width: 0;
    padding: 5px 0;
    background: transparent;
    border: none;
    color: #e5e7eb;
    font-size: 13px;
    font-family: inherit;
  }

  .tracker-search input:focus {
    outline: none;
  }

  .tracker-body {
    flex: 1;
    overflow-y: auto;
    padding: 10px 16px;
  }

  .issue-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .issue {
    padding: 8px 10px;
    border-radius: 7px;
    background: rgba(255, 255, 255, 0.03);
    border: 1px solid rgba(255, 255, 255, 0.06);
  }

  .issue.expanded {
    border-color: rgba(var(--accent-rgb), 0.35);
  }

  .issue-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .issue-main {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: baseline;
    gap: 8px;
    background: none;
    border: none;
    padding: 0;
    text-align: left;
    cursor: pointer;
    color: inherit;
    font: inherit;
  }

  .issue-ref {
    font-size: 12px;
    color: var(--accent, #818cf8);
    font-variant-numeric: tabular-nums;
    flex-shrink: 0;
  }

  .issue-title {
    font-size: 13px;
    color: #e4e4e7;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .issue.expanded .issue-title {
    white-space: normal;
  }

  .issue-actions {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-shrink: 0;
  }

  .issue-meta {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    margin-top: 4px;
    font-size: 11px;
    color: #8b8b93;
  }

  .chip {
    padding: 1px 7px;
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.06);
    color: #c4c4cc;
  }

  .overdue {
    color: #f87171;
  }

  .issue-description {
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid rgba(255, 255, 255, 0.06);
    font-size: 12px;
    line-height: 1.5;
    color: #d4d4d8;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    max-height: 320px;
    overflow-y: auto;
  }

  .icon-btn {
    background: transparent;
    border: 1px solid transparent;
    color: #a1a1aa;
    border-radius: 5px;
    width: 26px;
    height: 26px;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0;
  }

  .icon-btn:hover:not(:disabled) {
    background: rgba(255, 255, 255, 0.07);
    color: #e4e4e7;
  }

  .icon-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .send-btn {
    padding: 4px 10px;
    border-radius: 6px;
    border: 1px solid rgba(var(--accent-rgb), 0.4);
    background: rgba(var(--accent-rgb), 0.12);
    color: #e4e4e7;
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
  }

  .send-btn:hover:not(:disabled) {
    background: rgba(var(--accent-rgb), 0.22);
  }

  .send-btn:disabled {
    opacity: 0.45;
    cursor: default;
  }

  .spinning {
    animation: spin 0.9s linear infinite;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  .empty {
    text-align: center;
    color: #71717a;
    font-size: 12px;
    padding: 26px 0;
  }

  .error-line {
    background: rgba(248, 113, 113, 0.1);
    border: 1px solid rgba(248, 113, 113, 0.3);
    color: #fca5a5;
    border-radius: 6px;
    padding: 8px 10px;
    font-size: 12px;
    margin-bottom: 10px;
  }
</style>
