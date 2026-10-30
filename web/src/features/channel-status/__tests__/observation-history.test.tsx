import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'

import { RouteHistory } from '../components/route-history'
import type { ChannelHistory } from '../types'

const oldest = {
  id: 'real-oldest',
  source: 'real',
  status: 'healthy',
  observed_at: 1_797_498_001,
  order_ms: 1_797_498_001_001,
  ttft_ms: 420,
  latency_ms: 1820,
  output_tokens: 50,
} as const
const next = {
  ...oldest,
  id: 'real-next',
  observed_at: oldest.observed_at + 10,
  order_ms: oldest.order_ms + 10_000,
  ttft_ms: 510,
}
const probe = {
  ...oldest,
  id: 'probe-newest',
  source: 'probe' as const,
  observed_at: oldest.observed_at + 600,
  order_ms: oldest.order_ms + 600_000,
  ttft_ms: 250,
}

afterEach(cleanup)

describe('individual observation history', () => {
  it('draws adjacent completed observations with left padding and independent real samples', () => {
    render(
      <RouteHistory
        locale='en'
        history={
          {
            observation_limit: 60,
            real_sample_limit: 30,
            observations: [probe, next, oldest],
            real_samples: [next, oldest],
          } as ChannelHistory
        }
      />
    )
    const status = screen.getByRole('group', { name: 'Status history' })
    const real = screen.getByRole('group', {
      name: 'Real request first-token history',
    })
    const statusBars = within(status).getAllByRole('button')
    const realBars = within(real).getAllByRole('button')
    expect(status).toHaveClass('h-8')
    expect(real).toHaveClass('h-8')
    expect(statusBars).toHaveLength(60)
    expect(realBars).toHaveLength(30)
    expect(
      statusBars.slice(0, 57).every((bar) => bar.dataset.status === 'no_data')
    ).toBe(true)
    expect(statusBars.slice(57).map((bar) => bar.dataset.status)).toEqual([
      'healthy',
      'healthy',
      'healthy',
    ])
    expect(statusBars[57]).toHaveAccessibleName(
      expect.stringContaining('420 ms')
    )
    expect(statusBars[58]).toHaveAccessibleName(
      expect.stringContaining('510 ms')
    )
    expect(statusBars[59]).toHaveAccessibleName(
      expect.stringContaining('Probe')
    )
    expect(realBars[28]).toHaveAccessibleName(expect.stringContaining('420 ms'))
    expect(realBars[29]).toHaveAccessibleName(expect.stringContaining('510 ms'))
    expect(screen.getByText('Latest 60 observations')).toBeVisible()
    expect(screen.getByText('Latest 30 real requests')).toBeVisible()
    expect(screen.getByText('100%')).toBeVisible()
  })

  it('keeps an error red without a measured first token and excludes placeholders from success rate', async () => {
    const failed = { ...next, status: 'error' as const, ttft_ms: undefined }
    const user = userEvent.setup()
    render(
      <RouteHistory
        locale='en'
        history={
          {
            observation_limit: 60,
            real_sample_limit: 30,
            observations: [failed, probe],
            real_samples: [failed],
          } as ChannelHistory
        }
      />
    )
    const firstTokenBars = within(
      screen.getByRole('group', {
        name: 'Real request first-token history',
      })
    ).getAllByRole('button')
    const statusBars = within(
      screen.getByRole('group', {
        name: 'Status history',
      })
    ).getAllByRole('button')
    expect(firstTokenBars[29]).toHaveAttribute('data-status', 'error')
    expect(statusBars[59]).toHaveAttribute('data-status', 'error')
    expect(screen.getByText('50%')).toBeVisible()
    firstTokenBars[0].focus()
    await user.keyboard('{End}')
    expect(firstTokenBars[29]).toHaveFocus()
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Error')
  })

  it('shows no success percentage for an empty raw history', () => {
    render(
      <RouteHistory
        locale='en'
        history={
          {
            observation_limit: 60,
            real_sample_limit: 30,
            observations: [],
            real_samples: [],
          } as ChannelHistory
        }
      />
    )
    expect(screen.getByText('No observations yet.')).toBeVisible()
    expect(screen.queryByText('100%')).not.toBeInTheDocument()
    expect(screen.getByText('0 observations')).toBeVisible()
  })
})
