/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ChannelStatusPage } from '../index'

const statusResponse = {
  success: true,
  data: {
    updated_at: 1_797_500_000,
    targets: [
      {
        group: 'standard',
        model: 'mhenwa-chat',
        status: 'healthy' as const,
        source: 'real' as const,
        observed_at: 1_797_499_900,
        ttft_ms: 420,
        latency_ms: 1820,
        tps: 35.5,
      },
    ],
  },
}

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <ChannelStatusPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'get')
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('channel status page', () => {
  it('loads the group/model route summary without exposing cache-rate data', async () => {
    let resolveRequest!: (value: { data: typeof statusResponse }) => void
    vi.mocked(api.get).mockReturnValueOnce(
      new Promise((resolve) => {
        resolveRequest = resolve
      }) as never
    )

    renderPage()
    expect(screen.getByText('Loading...')).toBeVisible()
    resolveRequest({ data: statusResponse })

    expect(await screen.findByText('mhenwa-chat')).toBeVisible()
    expect(screen.getByText('standard')).toBeVisible()
    expect(screen.getByText('Real request')).toBeVisible()
    expect(screen.getByText('420 ms')).toBeVisible()
    expect(screen.getByText('1.82 s')).toBeVisible()
    expect(screen.getByText('35.5 tok/s')).toBeVisible()
    expect(screen.queryByText(/cache rate/i)).not.toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/api/channel-status', {
      signal: expect.any(AbortSignal),
    })
  })

  it('shows an empty state when no monitored group/model targets are available', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...statusResponse,
        data: { updated_at: 1_797_500_000, targets: [] },
      },
    } as never)

    renderPage()

    expect(await screen.findByText('No monitored routes')).toBeVisible()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('offers retry after an error and renders the latest response after retry', async () => {
    vi.mocked(api.get)
      .mockRejectedValueOnce(new Error('network unavailable'))
      .mockResolvedValueOnce({ data: statusResponse } as never)
    const user = userEvent.setup()

    renderPage()

    expect(
      await screen.findByText('Unable to load service status')
    ).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('mhenwa-chat')).toBeVisible()
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('refreshes the status table from the explicit refresh control', async () => {
    const user = userEvent.setup()
    vi.mocked(api.get)
      .mockResolvedValueOnce({ data: statusResponse } as never)
      .mockResolvedValueOnce({
        data: {
          ...statusResponse,
          data: {
            ...statusResponse.data,
            targets: [
              {
                ...statusResponse.data.targets[0],
                model: 'mhenwa-chat-latest',
              },
            ],
          },
        },
      } as never)

    renderPage()
    expect(await screen.findByText('mhenwa-chat')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Refresh status' }))

    expect(await screen.findByText('mhenwa-chat-latest')).toBeVisible()
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
  })

  it('falls back safely for status values introduced by a newer server', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...statusResponse,
        data: {
          ...statusResponse.data,
          targets: [
            {
              ...statusResponse.data.targets[0],
              status: 'degraded',
              source: 'synthetic',
            },
          ],
        },
      },
    } as never)

    renderPage()

    expect(await screen.findByText('No data')).toBeVisible()
    expect(screen.getByText('Not observed')).toBeVisible()
  })

  it('keeps the last data visible when a refresh fails', async () => {
    const user = userEvent.setup()
    vi.mocked(api.get)
      .mockResolvedValueOnce({ data: statusResponse } as never)
      .mockRejectedValueOnce(new Error('temporary outage'))

    renderPage()
    expect(await screen.findByText('mhenwa-chat')).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Refresh status' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Refresh failed. Showing the last available status.'
    )
    expect(screen.getByText('mhenwa-chat')).toBeVisible()
  })
})
