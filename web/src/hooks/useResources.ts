import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { CreateProxy, CreateSolver, UpdateProxy, UpdateSolver } from "@/lib/api"
import { keys } from "@/lib/query"

// Global proxy + anti-bot-solver resources an indexer references by id. Kept
// together (one screen, one concept) but with independent query keys.

export function useProxies() {
  return useQuery({ queryKey: keys.proxies.all, queryFn: () => api.listProxies() })
}

export function useProxyMutations() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: keys.proxies.all })
  return {
    create: useMutation({ mutationFn: (body: CreateProxy) => api.createProxy(body), onSettled: invalidate }),
    update: useMutation({
      mutationFn: ({ id, body }: { id: number, body: UpdateProxy }) => api.updateProxy(id, body),
      onSettled: invalidate,
    }),
    // Deleting a proxy nulls any indexer's reference (ON DELETE SET NULL), so
    // refresh the indexer list too.
    remove: useMutation({
      mutationFn: (id: number) => api.deleteProxy(id),
      onSettled: () => {
        void invalidate()
        void qc.invalidateQueries({ queryKey: keys.indexers.all })
      },
    }),
  }
}

export function useSolvers() {
  return useQuery({ queryKey: keys.solvers.all, queryFn: () => api.listSolvers() })
}

export function useSolverMutations() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: keys.solvers.all })
  return {
    create: useMutation({ mutationFn: (body: CreateSolver) => api.createSolver(body), onSettled: invalidate }),
    update: useMutation({
      mutationFn: ({ id, body }: { id: number, body: UpdateSolver }) => api.updateSolver(id, body),
      onSettled: invalidate,
    }),
    remove: useMutation({
      mutationFn: (id: number) => api.deleteSolver(id),
      onSettled: () => {
        void invalidate()
        void qc.invalidateQueries({ queryKey: keys.indexers.all })
      },
    }),
  }
}
