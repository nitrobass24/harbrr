import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api, APIError, type Credentials } from "@/lib/api"

// useAuth is the bootstrap: one me-query drives the guard, the login/setup
// screens, and the sidebar user chip. A 401 resolves to user=null (not an
// error state) so the guard can branch without retry storms.
export function useAuth() {
  const queryClient = useQueryClient()

  const me = useQuery({
    queryKey: ["auth", "me"],
    queryFn: async () => {
      try {
        return await api.getMe()
      } catch (err) {
        if (err instanceof APIError && err.status === 401) return null
        throw err
      }
    },
    retry: false,
    staleTime: Infinity,
  })

  // Only probed when unauthenticated: routes the visitor to /setup vs /login.
  const setup = useQuery({
    queryKey: ["auth", "setup"],
    queryFn: () => api.getSetup(),
    enabled: me.data === null,
    retry: false,
  })

  const login = useMutation({
    mutationFn: (creds: Credentials) => api.login(creds),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["auth"] }),
  })

  const logout = useMutation({
    mutationFn: () => api.logout(),
    onSettled: () => {
      api.setCsrfToken("")
      queryClient.clear()
      // Full-page navigation so every in-memory state resets.
      api.onUnauthorized()
    },
  })

  return {
    user: me.data ?? null,
    isLoading: me.isLoading,
    isAuthenticated: Boolean(me.data),
    authDisabled: me.data?.authMethod === "disabled",
    setupComplete: setup.data?.setupComplete,
    setupLoading: me.data === null && setup.isLoading,
    login,
    logout,
  }
}
