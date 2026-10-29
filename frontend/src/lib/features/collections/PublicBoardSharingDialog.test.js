import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) =>
    ({
      'collections.publicBoardSharing.title': 'Öffentliches Board teilen',
      'collections.publicBoardSharing.subtitle':
        'Veröffentlichen Sie diese Sammlung als schreibgeschütztes Kanban-Board.',
      'collections.publicBoardSharing.enable': 'Öffentliches Teilen aktivieren',
      'collections.publicBoardSharing.enableHint':
        'Jeder mit dem Link kann das Board ohne Anmeldung ansehen.',
      'collections.publicBoardSharing.boardUrl': 'Board-URL',
      'collections.publicBoardSharing.slugHint':
        'Verwenden Sie 3–64 Kleinbuchstaben, Ziffern und Bindestriche.',
      'collections.publicBoardSharing.copyLink': 'Öffentlichen Link kopieren',
      'collections.publicBoardSharing.copied': 'Kopiert!',
      'collections.publicBoardSharing.openBoard': 'Board öffnen',
      'collections.publicBoardSharing.milestoneSwimlanes': 'Karten nach Meilenstein gruppieren',
      'collections.publicBoardSharing.milestoneSwimlanesHint':
        'Öffentliche Besucher sehen Meilenstein-Swimlanes.',
      'collections.publicBoardSharing.scopeNote':
        'Die Sammlungsabfrage muss einen workspaceKey-Bereich enthalten. Sie müssen in jedem referenzierten Arbeitsbereich Arbeitsbereichsadministrator sein.',
      'collections.publicBoardSharing.save': 'Freigabe speichern',
      'collections.publicBoardSharing.disable': 'Freigabe deaktivieren',
      'collections.publicBoardSharing.saveError':
        'Die Einstellungen für das öffentliche Teilen konnten nicht gespeichert werden.',
      'dialogs.cancel': 'Abbrechen',
      'common.saving': 'Wird gespeichert...',
    })[key] ?? key,
}));

beforeAll(() => {
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

import PublicBoardSharingDialog from './PublicBoardSharingDialog.svelte';

afterEach(() => {
  document.body.innerHTML = '';
});

describe('PublicBoardSharingDialog', () => {
  it('renders translated copy instead of hardcoded English', async () => {
    render(PublicBoardSharingDialog, {
      props: {
        isOpen: true,
        isPublic: true,
        publicSlug: 'test-board',
        slugSaved: true,
      },
    });

    expect(await screen.findByText('Öffentliches Board teilen')).toBeTruthy();
    expect(
      screen.getByText('Veröffentlichen Sie diese Sammlung als schreibgeschütztes Kanban-Board.')
    ).toBeTruthy();
    expect(screen.getByText('Öffentliches Teilen aktivieren')).toBeTruthy();
    expect(
      screen.getByText('Jeder mit dem Link kann das Board ohne Anmeldung ansehen.')
    ).toBeTruthy();
    expect(screen.getByText('Board-URL')).toBeTruthy();
    expect(screen.getByText('Öffentlichen Link kopieren')).toBeTruthy();
    expect(screen.getByText('Board öffnen')).toBeTruthy();
    expect(
      screen.getByText(
        'Die Sammlungsabfrage muss einen workspaceKey-Bereich enthalten. Sie müssen in jedem referenzierten Arbeitsbereich Arbeitsbereichsadministrator sein.'
      )
    ).toBeTruthy();
    expect(screen.getByText('Freigabe speichern')).toBeTruthy();
    expect(screen.getByText('Abbrechen')).toBeTruthy();

    expect(screen.queryByText('Share public board')).toBeNull();
    expect(screen.queryByText('Enable public sharing')).toBeNull();
  });
});


describe('PublicBoardSharingDialog milestone swimlanes', () => {
  it('submits the owner-configured milestone swimlane setting', async () => {
    const onsave = vi.fn().mockResolvedValue({ ok: true });
    render(PublicBoardSharingDialog, {
      props: {
        isOpen: true,
        isPublic: true,
        publicSlug: 'test-board',
        publicMilestoneSwimlanes: false,
        slugSaved: true,
        onsave,
      },
    });

    const checkbox = await screen.findByTestId('public-board-milestone-swimlanes');
    fireEvent.click(checkbox);
    fireEvent.click(screen.getByTestId('public-board-save'));

    expect(onsave).toHaveBeenCalledWith({
      isPublic: true,
      publicSlug: 'test-board',
      publicMilestoneSwimlanes: true,
    });
  });
});
