import { describe, expect, it } from 'vitest'

import type { ChannelStatusTarget } from '../types'
import { aggregateChannelStatusByGroup } from './group-status'

function target(
  overrides: Partial<ChannelStatusTarget> = {}
): ChannelStatusTarget {
  return {
    group: 'standard',
    model: 'chat',
    status: 'healthy',
    source: 'real',
    history: {
      observation_limit: 60,
      real_sample_limit: 30,
      observations: [],
      real_samples: [],
    },
    ...overrides,
  }
}

describe('aggregateChannelStatusByGroup', () => {
  it('prioritizes errors and weights success rate by observation count', () => {
    const result = aggregateChannelStatusByGroup([
      target({
        model: 'chat',
        history: {
          observation_limit: 60,
          real_sample_limit: 30,
          observations: [
            {
              id: 'chat-1',
              source: 'real',
              status: 'healthy',
              observed_at: 1,
              order_ms: 1,
            },
            {
              id: 'chat-2',
              source: 'probe',
              status: 'error',
              observed_at: 2,
              order_ms: 2,
            },
          ],
          real_samples: [],
        },
      }),
      target({
        model: 'reasoner',
        status: 'error',
        history: {
          observation_limit: 60,
          real_sample_limit: 30,
          observations: [
            {
              id: 'reasoner-1',
              source: 'probe',
              status: 'healthy',
              observed_at: 3,
              order_ms: 3,
            },
          ],
          real_samples: [],
        },
      }),
    ]).get('standard')

    expect(result).toMatchObject({
      status: 'error',
      observationCount: 3,
      successRate: (2 / 3) * 100,
    })
  })

  it('keeps unobserved monitored groups visible without a rate', () => {
    const result = aggregateChannelStatusByGroup([
      target({ status: 'no_data' }),
    ]).get('standard')

    expect(result).toEqual({
      status: 'no_data',
      successRate: undefined,
      observationCount: 0,
    })
  })
})
