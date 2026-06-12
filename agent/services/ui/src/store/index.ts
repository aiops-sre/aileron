import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { Cluster } from '@/api/kubesense';

interface UIState {
  // Cluster selection
  clusters: Cluster[];
  activeClusterId: string | null;
  activeNamespace: string;
  setClusters: (c: Cluster[]) => void;
  setActiveCluster: (id: string) => void;
  setNamespace: (ns: string) => void;

  // Command palette
  commandPaletteOpen: boolean;
  setCommandPaletteOpen: (v: boolean) => void;

  // Active investigation
  activeInvestigationId: string | null;
  setActiveInvestigation: (id: string | null) => void;

  // Terminal visibility
  terminalOpen: boolean;
  setTerminalOpen: (v: boolean) => void;

  // API config
  apiUrl: string;
  apiToken: string;
  setApiConfig: (url: string, token: string) => void;
}

export const useStore = create<UIState>()(
  persist(
    (set) => ({
      clusters: [],
      activeClusterId: null,
      activeNamespace: 'all',
      setClusters: (clusters) => set({ clusters }),
      setActiveCluster: (id) => set({ activeClusterId: id }),
      setNamespace: (ns) => set({ activeNamespace: ns }),

      commandPaletteOpen: false,
      setCommandPaletteOpen: (v) => set({ commandPaletteOpen: v }),

      activeInvestigationId: null,
      setActiveInvestigation: (id) => set({ activeInvestigationId: id }),

      terminalOpen: false,
      setTerminalOpen: (v) => set({ terminalOpen: v }),

      apiUrl: '',
      apiToken: '',
      setApiConfig: (apiUrl, apiToken) => {
        localStorage.setItem('ks_token', apiToken);
        set({ apiUrl, apiToken });
      },
    }),
    { name: 'kubesense-ui', partialize: (s) => ({ activeClusterId: s.activeClusterId, activeNamespace: s.activeNamespace, apiUrl: s.apiUrl, apiToken: s.apiToken }) }
  )
);
