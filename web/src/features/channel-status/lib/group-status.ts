import type { ChannelStatus, ChannelStatusTarget } from '../types'

export interface ChannelGroupStatus {
  status: ChannelStatus
  successRate?: number
  observationCount: number
}

const statusPriority: Record<ChannelStatus, number> = {
  error: 4,
  degraded: 3,
  healthy: 2,
  no_data: 1,
}

/**
 * Collapse model-level observations into the status shown beside a user group.
 * A group is degraded when any monitored model is degraded, and its success
 * rate is weighted by the number of observations rather than averaged per
 * model.
 */
export function aggregateChannelStatusByGroup(
  targets: ChannelStatusTarget[]
): Map<string, ChannelGroupStatus> {
  const result = new Map<string, ChannelGroupStatus>()
  const samplesByGroup = new Map<string, Set<string>>()
  const successCounts = new Map<string, number>()

  for (const target of targets) {
    const current = result.get(target.group)
    const status =
      !current || statusPriority[target.status] > statusPriority[current.status]
        ? target.status
        : current.status
    const sampleKeys = samplesByGroup.get(target.group) ?? new Set<string>()
    let observationCount = current?.observationCount ?? 0
    let successCount = successCounts.get(target.group) ?? 0

    for (const sample of target.history?.observations ?? []) {
      const key = `${target.model}\u0000${sample.id}`
      if (sampleKeys.has(key)) continue
      sampleKeys.add(key)
      observationCount += 1
      if (sample.status !== 'error') successCount += 1
    }

    samplesByGroup.set(target.group, sampleKeys)
    successCounts.set(target.group, successCount)
    result.set(target.group, {
      status,
      successRate:
        observationCount > 0
          ? (successCount / observationCount) * 100
          : undefined,
      observationCount,
    })
  }

  return result
}
