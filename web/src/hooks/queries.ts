import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { Job } from '@/lib/types'

export function useJobs() {
  return useQuery({ queryKey: ['jobs'], queryFn: () => api.listJobs({ limit: '200' }).then((r) => r.jobs) })
}

export function useJob(id: string | undefined) {
  return useQuery({
    queryKey: ['job', id],
    queryFn: () => api.getJob(id!),
    enabled: !!id,
  })
}

export function useCreateJob() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (j: Partial<Job>) => api.createJob(j),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jobs'] }),
  })
}

export function useUpdateJob() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, job }: { id: string; job: Partial<Job> }) => api.updateJob(id, job),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jobs'] }),
  })
}

export function useDeleteJob() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.deleteJob(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jobs'] }),
  })
}

export function useRunJob() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.runJob(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['runs'] })
      qc.invalidateQueries({ queryKey: ['stats'] })
    },
  })
}

export function useRuns(params: Record<string, string> = {}) {
  return useQuery({
    queryKey: ['runs', JSON.stringify(params)],
    queryFn: () => api.listRuns(params).then((r) => r.runs),
  })
}

export function useRun(id: string | undefined) {
  return useQuery({
    queryKey: ['run', id],
    queryFn: () => api.getRun(id!),
    enabled: !!id,
  })
}

export function useStats() {
  return useQuery({
    queryKey: ['stats'],
    queryFn: () => api.getStats(),
    refetchInterval: 5000,
  })
}
export function useAgents() {
  return useQuery({ queryKey: ['agents'], queryFn: () => api.listAgents().then((r) => r.agents ?? []), refetchInterval: 15_000 })
}

export function useSSHHosts() {
  return useQuery({ queryKey: ['ssh-hosts'], queryFn: () => api.listSSHHosts().then((r) => r.hosts ?? []) })
}

export function useChannels() {
  return useQuery({ queryKey: ['channels'], queryFn: () => api.listChannels().then((r) => r.channels ?? []) })
}

// Latest run per job, from the most recent runs page.
export function useLatestRuns(limit = 200) {
  return useQuery({
    queryKey: ['runs', 'latest', limit],
    queryFn: () => api.listRuns({ limit: String(limit) }).then((r) => r.runs),
    refetchInterval: 10_000,
    select: (runs) => {
      const byJob = new Map<string, (typeof runs)[number]>()
      for (const r of runs) if (!byJob.has(r.job_id)) byJob.set(r.job_id, r)
      return { runs, byJob }
    },
  })
}

export function useStorageLocations() {
  return useQuery({ queryKey: ['storage-locations'], queryFn: () => api.listLocations().then((r) => r.locations) })
}

export function useWorkflows() {
  return useQuery({ queryKey: ['workflows'], queryFn: () => api.listWorkflows().then((r) => r.workflows ?? []) })
}
