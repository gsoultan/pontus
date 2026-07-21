import { create } from "zustand";
import { persist } from "zustand/middleware";

interface AuthState {
  token: string;
  username: string;
  role: string;
  setAuth: (token: string, username: string, role: string) => void;
  isAuthenticated: boolean;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      token: "",
      username: "",
      role: "",
      isAuthenticated: false,
      setAuth: (token: string, username: string, role: string) =>
        set({ token, username, role, isAuthenticated: !!token }),
      logout: () => set({ token: "", username: "", role: "", isAuthenticated: false }),
    }),
    {
      name: "pontus-auth",
    }
  )
);
