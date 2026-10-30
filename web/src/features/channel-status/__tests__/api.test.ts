import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { getChannelStatus } from '../api'

const sample = {
  id: 'real-1',
  source: 'real',
  status: 'healthy',
  observed_at: 1_797_498_001,
  order_ms: 1_797_498_001_001,
  ttft_ms: 420,
  latency_ms: 1820,
}

afterEach(() => vi.restoreAllMocks())

describe('individual channel observation response', () => {
  it('sorts and deduplicates bounded mixed observations and preserves independent real samples', async () => {
    const probes = Array.from({ length: 65 }, (_, index) => ({
      ...sample,
      id: `probe-${index}`,
      source: 'probe',
      order_ms: sample.order_ms + index + 1,
    }))
    const realSamples = Array.from({ length: 35 }, (_, index) => ({
      ...sample,
      id: `real-${index}`,
      order_ms: sample.order_ms - index,
    }))
    vi.spyOn(api, 'get').mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          updated_at: sample.observed_at,
          targets: [
            {
              group: 'standard',
              model: 'chat',
              status: 'healthy',
              source: 'probe',
              history: {
                observations: [...probes, probes[64]],
                real_samples: [probes[64], ...realSamples, realSamples[0]],
                sample_count: 999,
                success_rate: 0,
              },
            },
          ],
        },
      },
    } as never)
    const response = await getChannelStatus()
    const history = response.data?.targets[0].history
    expect(history?.observations).toHaveLength(60)
    expect(history?.observations[0].id).toBe('probe-64')
    expect(history?.observations[59].id).toBe('probe-5')
    expect(history?.real_samples).toHaveLength(30)
    expect(history?.real_samples[0].id).toBe('real-0')
    expect(history?.real_samples[29].id).toBe('real-29')
  })

  it('ignores invalid samples without converting legacy minute buckets into requests', async () => {
    vi.spyOn(api, 'get').mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          updated_at: sample.observed_at,
          targets: [
            {
              group: 'standard',
              model: 'chat',
              status: 'healthy',
              source: 'real',
              history: { buckets: [{ sample_count: 20 }], sample_count: 20 },
            },
            {
              group: 'premium',
              model: 'chat',
              status: 'error',
              source: 'real',
              history: {
                observations: [
                  { ...sample, id: 'bad-status', status: 'unknown' },
                  { ...sample, id: 'bad-time', order_ms: -1 },
                  { ...sample, id: 'bad-source', source: 'none' },
                  { ...sample, id: '', status: 'error' },
                  {
                    ...sample,
                    status: 'error',
                    ttft_ms: -10,
                    output_tokens: 0,
                  },
                ],
                real_samples: [],
              },
            },
          ],
        },
      },
    } as never)
    const response = await getChannelStatus()
    expect(response.data?.targets[0].history).toBeUndefined()
    expect(response.data?.targets[0].status).toBe('healthy')
    const observations = response.data?.targets[1].history?.observations
    expect(observations).toHaveLength(1)
    expect(observations?.[0]).toMatchObject({
      status: 'error',
      output_tokens: 0,
    })
    expect(observations?.[0].ttft_ms).toBeUndefined()
  })
})
