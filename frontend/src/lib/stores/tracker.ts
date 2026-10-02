import { writable, get } from 'svelte/store';
import * as App from '../../../wailsjs/go/main/App';
import type { main } from '../../../wailsjs/go/models';
import { activeProjectId } from './projects';
import { selectedSessionId } from './sessions';

/**
 * The external issue tracker the selected session reads its work from — its
 * own binding, or the project's — or null when it uses the built-in task list.
 *
 * The Tasks view shows the tracker's issues instead of the built-in list while
 * this is set. Reloaded on every project and session switch, and after a
 * binding changes.
 */
export const trackerBinding = writable<main.TrackerBindingInfo | null>(null);

let generation = 0;

export async function refreshTrackerBinding(): Promise<void> {
  const projectId = get(activeProjectId);
  const sessionId = get(selectedSessionId) || '';
  const current = ++generation;
  try {
    const binding = await App.GetTrackerBinding(projectId, sessionId);
    if (current !== generation) return;
    trackerBinding.set(binding && binding.connectionId ? binding : null);
  } catch (e) {
    if (current !== generation) return;
    // An unreadable trackers.json falls back to the built-in list rather than
    // leaving the Tasks view empty.
    console.error('Failed to load the tracker binding:', e);
    trackerBinding.set(null);
  }
}

activeProjectId.subscribe(() => {
  void refreshTrackerBinding();
});

selectedSessionId.subscribe(() => {
  void refreshTrackerBinding();
});
