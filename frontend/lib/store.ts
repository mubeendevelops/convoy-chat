import { create } from "zustand";

// UI-only state that doesn't belong in React Query (server cache) or
// component-local state. Future slices (e.g. auth in Phase 10) get added
// the same way: extend this interface and the create() initializer below.
interface UIState {
  sidebarOpen: boolean;
  setSidebarOpen: (open: boolean) => void;
  toggleSidebar: () => void;
}

export const useUIStore = create<UIState>((set) => ({
  sidebarOpen: false,
  setSidebarOpen: (open) => set({ sidebarOpen: open }),
  toggleSidebar: () => set((state) => ({ sidebarOpen: !state.sidebarOpen })),
}));
