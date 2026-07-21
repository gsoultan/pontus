import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface ProjectStore {
  selectedProjectId: string | null;
  selectedProxyId: string | null;
  setSelectedProjectId: (id: string | null) => void;
  setSelectedProxyId: (id: string | null) => void;
}

export const useProjectStore = create<ProjectStore>()(
  persist(
    (set) => ({
      selectedProjectId: null,
      selectedProxyId: null,
      setSelectedProjectId: (id) => set({ selectedProjectId: id, selectedProxyId: null }),
      setSelectedProxyId: (id) => set({ selectedProxyId: id }),
    }),
    {
      name: 'pontus-project-storage',
    }
  )
);
