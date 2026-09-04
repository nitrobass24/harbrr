import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api, unwrap } from "@/lib/api"
import type { CreateProxy, CreateSolver, UpdateProxy, UpdateSolver } from "@/lib/api"
import { keys } from "@/lib/query"

// Global proxy + anti-bot-solver resources an indexer references by id. Kept
// together (one screen, one concept) but with independent query keys.

export function useProxies() {
  return useQuery({ queryKey: keys.proxies.all, queryFn: () => unwrap(api.http.GET("/api/proxies")) })
}

export function useProxyMutations() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: keys.proxies.all })
  return {
    create: useMutation({ mutationFn: (body: CreateProxy) => unwrap(api.http.POST("/api/proxies", { body: body })), onSettled: invalidate }),
    update: useMutation({
      mutationFn: ({ id, body }: { id: number, body: UpdateProxy }) => unwrap(api.http.PATCH("/api/proxies/{id}", { params: { path: { id } }, body })),
      onSettled: invalidate,
    }),
    // Deleting a proxy nulls any indexer's reference (ON DELETE SET NULL), so
    // refresh the indexer list too.
    remove: useMutation({
      mutationFn: (id: number) => unwrap(api.http.DELETE("/api/proxies/{id}", { params: { path: { id } } })),
      onSettled: () => {
        void invalidate()
        void qc.invalidateQueries({ queryKey: keys.indexers.all })
      },
    }),
  }
}

export function useSolvers() {
  return useQuery({ queryKey: keys.solvers.all, queryFn: () => unwrap(api.http.GET("/api/solvers")) })
}

export function useSolverMutations() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: keys.solvers.all })
  return {
    create: useMutation({ mutationFn: (body: CreateSolver) => unwrap(api.http.POST("/api/solvers", { body: body })), onSettled: invalidate }),
    update: useMutation({
      mutationFn: ({ id, body }: { id: number, body: UpdateSolver }) => unwrap(api.http.PATCH("/api/solvers/{id}", { params: { path: { id } }, body })),
      onSettled: invalidate,
    }),
    remove: useMutation({
      mutationFn: (id: number) => unwrap(api.http.DELETE("/api/solvers/{id}", { params: { path: { id } } })),
      onSettled: () => {
        void invalidate()
        void qc.invalidateQueries({ queryKey: keys.indexers.all })
      },
    }),
  }
}
