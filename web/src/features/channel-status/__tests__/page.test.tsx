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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AxiosAdapter } from 'axios'
import { StrictMode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { Route as SettingsRoute } from '@/routes/_authenticated/channel-status/settings'
import { useAuthStore } from '@/stores/auth-store'

import { ChannelStatusAdminPage } from '../admin-page'
import { ChannelStatusPage } from '../index'

const originalAdapter = api.defaults.adapter

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
        output_tokens: 50,
        effective_ratio: 0.035,
      },
    ],
  },
}

function renderPage(strictMode = false) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const page = (
    <QueryClientProvider client={queryClient}>
      <ChannelStatusPage />
    </QueryClientProvider>
  )
  return render(strictMode ? <StrictMode>{page}</StrictMode> : page)
}

beforeEach(() => {
  vi.spyOn(api, 'get')
  vi.spyOn(api, 'put')
  useAuthStore.getState().auth.reset()
})

const monitorResponse = {
  success: true,
  data: {
    source: 'database',
    targets: [
      {
        group: 'standard',
        model: 'mhenwa-chat',
        probe_token_id: 21,
        interval_seconds: 300,
        timeout_seconds: 30,
      },
    ],
    groups: ['standard', 'premium'],
    models_by_group: {
      standard: ['mhenwa-chat', 'mhenwa-reasoner'],
      premium: ['mhenwa-pro'],
    },
    probe_tokens: [
      {
        id: 21,
        name: 'chat probe',
        group: 'standard',
        models: ['mhenwa-chat'],
      },
      {
        id: 22,
        name: 'reasoner probe',
        group: 'standard',
        models: ['mhenwa-reasoner'],
      },
      {
        id: 23,
        name: 'premium probe',
        group: 'premium',
        models: ['mhenwa-pro'],
      },
    ],
  },
}

function renderAdminPage(protectedRoute = false, strictMode = false) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <Outlet />
      </QueryClientProvider>
    ),
  })
  const settingsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/channel-status/settings',
    beforeLoad: protectedRoute
      ? () => SettingsRoute.options.beforeLoad?.({} as never)
      : undefined,
    component: ChannelStatusAdminPage,
  })
  const dashboardRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/dashboard',
    component: () => <p>Dashboard</p>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([settingsRoute, dashboardRoute]),
    history: createMemoryHistory({
      initialEntries: ['/channel-status/settings'],
    }),
  })
  const page = <RouterProvider router={router} />
  return {
    queryClient,
    ...render(strictMode ? <StrictMode>{page}</StrictMode> : page),
  }
}

describe('channel status settings page', () => {
  it('loads settings through the real HTTP client when StrictMode cancels and restarts its first request', async () => {
    const adapter = vi.fn<AxiosAdapter>(async (config) => ({
      data: monitorResponse,
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }))
    api.defaults.adapter = adapter

    renderAdminPage(false, true)

    expect(
      await screen.findByRole('spinbutton', {
        name: 'Maximum idle interval (seconds) 1',
      })
    ).toHaveValue(300)
    expect(api.get).toHaveBeenCalledTimes(2)
    expect(vi.mocked(api.get).mock.calls[0][1]?.signal?.aborted).toBe(true)
    expect(vi.mocked(api.get).mock.calls[1][1]?.signal?.aborted).toBe(false)
    expect(
      screen.queryByText('Unable to load channel status settings.')
    ).not.toBeInTheDocument()
  })

  it('redirects a non-admin direct URL before requesting settings', async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'viewer', role: ROLE.USER })

    renderAdminPage(true)

    expect(await screen.findByText('Dashboard')).toBeVisible()
    expect(api.get).not.toHaveBeenCalled()
    expect(api.put).not.toHaveBeenCalled()
  })

  it('allows administrators through the settings route guard', async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 2, username: 'administrator', role: ROLE.ADMIN })
    vi.mocked(api.get).mockResolvedValueOnce({ data: monitorResponse } as never)

    renderAdminPage(true)

    expect(
      await screen.findByRole('spinbutton', {
        name: 'Maximum idle interval (seconds) 1',
      })
    ).toHaveValue(300)
    expect(api.get).toHaveBeenCalledWith('/api/admin/channel-status/targets', {
      signal: expect.any(AbortSignal),
      disableDuplicate: true,
    })
  })

  it('blocks saving after the initial settings request fails and enables editing after retry', async () => {
    vi.mocked(api.get)
      .mockRejectedValueOnce(new Error('network unavailable'))
      .mockResolvedValueOnce({ data: monitorResponse } as never)
    const user = userEvent.setup()

    renderAdminPage()

    expect(
      await screen.findByText('Unable to load channel status settings.')
    ).toBeVisible()
    expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Add route' })).toBeDisabled()
    expect(api.put).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(
      await screen.findByRole('spinbutton', {
        name: 'Maximum idle interval (seconds) 1',
      })
    ).toHaveValue(300)
    expect(screen.getByRole('button', { name: 'Add route' })).toBeEnabled()
  })

  it('allows saving the loaded environment fallback to take ownership without altering its targets', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...monitorResponse,
        data: { ...monitorResponse.data, source: 'environment' },
      },
    } as never)
    vi.mocked(api.put).mockResolvedValueOnce({ data: monitorResponse } as never)
    const user = userEvent.setup()
    renderAdminPage()
    expect(
      await screen.findByText('Environment fallback (save to take ownership)')
    ).toBeVisible()
    await screen.findByRole('spinbutton', {
      name: 'Maximum idle interval (seconds) 1',
    })
    await user.click(screen.getByRole('button', { name: 'Save settings' }))

    expect(await screen.findByText('Database configuration')).toBeVisible()
    expect(api.put).toHaveBeenCalledWith(
      '/api/admin/channel-status/targets',
      { targets: monitorResponse.data.targets },
      { skipBusinessError: true, skipErrorHandler: true }
    )
  })

  it('rejects a success response with missing settings instead of enabling an empty overwrite', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: { success: true },
    } as never)

    renderAdminPage()

    expect(
      await screen.findByText('Unable to load channel status settings.')
    ).toBeVisible()
    expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Add route' })).toBeDisabled()
  })

  it('accepts an unconfigured null list and enables adding the first route', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...monitorResponse,
        data: { ...monitorResponse.data, source: 'none', targets: null },
      },
    } as never)
    const user = userEvent.setup()

    renderAdminPage()

    expect(await screen.findByText('No monitored routes')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Add route' }))
    expect(
      screen.getByRole('spinbutton', {
        name: 'Maximum idle interval (seconds) 1',
      })
    ).toHaveValue(300)
    expect(
      screen.getByRole('combobox', { name: 'Probe key 1' })
    ).toHaveTextContent('chat probe')
  })

  it('uses mobile native selectors and submits the model and key chosen after changing groups', async () => {
    const matchMedia = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => {
      const result = matchMedia(query)
      return {
        ...result,
        matches: query === '(max-width: 640px)' || result.matches,
      }
    })
    const selected = {
      group: 'premium',
      model: 'mhenwa-pro',
      probe_token_id: 24,
      interval_seconds: 300,
      timeout_seconds: 30,
    }
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...monitorResponse,
        data: {
          ...monitorResponse.data,
          probe_tokens: [
            ...monitorResponse.data.probe_tokens,
            {
              id: 24,
              name: 'premium backup',
              group: 'premium',
              models: ['mhenwa-pro'],
            },
          ],
        },
      },
    } as never)
    vi.mocked(api.put).mockResolvedValueOnce({
      data: {
        ...monitorResponse,
        data: { ...monitorResponse.data, targets: [selected] },
      },
    } as never)
    const user = userEvent.setup()

    renderAdminPage()

    const group = await screen.findByRole('combobox', { name: 'Group 1' })
    const model = screen.getByRole('combobox', { name: 'Model 1' })
    const key = screen.getByRole('combobox', { name: 'Probe key 1' })
    expect(group).toHaveDisplayValue('standard')
    expect(model).toHaveDisplayValue('mhenwa-chat')
    expect(key).toHaveDisplayValue('chat probe')
    await user.selectOptions(model, 'mhenwa-reasoner')
    expect(key).toHaveValue('22')
    await user.selectOptions(group, 'premium')
    expect(model).toHaveValue('mhenwa-pro')
    expect(key).toHaveValue('23')
    expect(
      screen.queryByRole('option', { name: 'chat probe' })
    ).not.toBeInTheDocument()
    await user.selectOptions(key, '24')
    await user.click(screen.getByRole('button', { name: 'Save settings' }))

    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith(
        '/api/admin/channel-status/targets',
        { targets: [selected] },
        { skipBusinessError: true, skipErrorHandler: true }
      )
    )
  })

  it('shows the mobile key placeholder for a removed key and submits the single replacement key after selection', async () => {
    const matchMedia = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => {
      const result = matchMedia(query)
      return {
        ...result,
        matches: query === '(max-width: 640px)' || result.matches,
      }
    })
    const selected = { ...monitorResponse.data.targets[0], probe_token_id: 22 }
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...monitorResponse,
        data: {
          ...monitorResponse.data,
          probe_tokens: [
            {
              id: 22,
              name: 'replacement probe',
              group: 'standard',
              models: ['mhenwa-chat'],
            },
          ],
        },
      },
    } as never)
    vi.mocked(api.put).mockResolvedValueOnce({
      data: {
        ...monitorResponse,
        data: { ...monitorResponse.data, targets: [selected] },
      },
    } as never)
    const user = userEvent.setup()
    renderAdminPage()

    const key = await screen.findByRole('combobox', { name: 'Probe key 1' })
    expect(key).toHaveValue('')
    expect(key).toHaveDisplayValue('Select probe key')
    await user.selectOptions(key, '22')
    await user.click(screen.getByRole('button', { name: 'Save settings' }))

    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith(
        '/api/admin/channel-status/targets',
        { targets: [selected] },
        { skipBusinessError: true, skipErrorHandler: true }
      )
    )
  })

  it('updates model and eligible key selections when the group or model changes', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ data: monitorResponse } as never)
    const user = userEvent.setup()

    renderAdminPage()

    await user.click(await screen.findByRole('combobox', { name: 'Model 1' }))
    await user.click(
      await screen.findByRole('option', { name: 'mhenwa-reasoner' })
    )
    expect(
      screen.getByRole('combobox', { name: 'Probe key 1' })
    ).toHaveTextContent('reasoner probe')
    await user.click(screen.getByRole('combobox', { name: 'Group 1' }))
    await user.click(await screen.findByRole('option', { name: 'premium' }))
    expect(screen.getByRole('combobox', { name: 'Model 1' })).toHaveTextContent(
      'mhenwa-pro'
    )
    expect(
      screen.getByRole('combobox', { name: 'Probe key 1' })
    ).toHaveTextContent('premium probe')
    await user.click(screen.getByRole('button', { name: 'Add route' }))
    expect(screen.getByRole('combobox', { name: 'Group 2' })).toHaveTextContent(
      'standard'
    )
    await user.click(screen.getByRole('button', { name: 'Remove route 1' }))
    expect(
      screen.getByRole('combobox', { name: 'Probe key 1' })
    ).toHaveTextContent('chat probe')
    expect(
      screen.queryByRole('combobox', { name: 'Group 2' })
    ).not.toBeInTheDocument()
  })

  it.each([
    [
      'Maximum idle interval (seconds) 1',
      '59',
      'Interval must be between 60 and 3600 seconds',
    ],
    [
      'Maximum idle interval (seconds) 1',
      '3601',
      'Interval must be between 60 and 3600 seconds',
    ],
    [
      'Maximum idle interval (seconds) 1',
      '60.5',
      'Enter a whole number of seconds',
    ],
    ['Timeout (seconds) 1', '4', 'Timeout must be between 5 and 120 seconds'],
    ['Timeout (seconds) 1', '121', 'Timeout must be between 5 and 120 seconds'],
  ])(
    'blocks saving %s=%s and shows its field validation',
    async (name, value, message) => {
      vi.mocked(api.get).mockResolvedValueOnce({
        data: monitorResponse,
      } as never)
      const user = userEvent.setup()

      renderAdminPage()
      const input = await screen.findByRole('spinbutton', { name })
      await user.clear(input)
      await user.type(input, value)
      await user.click(screen.getByRole('button', { name: 'Save settings' }))

      expect(await screen.findByText(message)).toBeVisible()
      expect(input).toHaveAttribute('aria-invalid', 'true')
      expect(api.put).not.toHaveBeenCalled()
    }
  )

  it('rejects duplicate group/model routes and a reused probe key', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ data: monitorResponse } as never)
    const user = userEvent.setup()

    renderAdminPage()
    await screen.findByRole('combobox', { name: 'Model 1' })
    await user.click(screen.getByRole('button', { name: 'Add route' }))
    await user.click(screen.getByRole('combobox', { name: 'Model 2' }))
    await user.click(await screen.findByRole('option', { name: 'mhenwa-chat' }))
    await user.click(screen.getByRole('button', { name: 'Save settings' }))

    expect(
      await screen.findByText('Each group and model route must be unique')
    ).toBeVisible()
    expect(
      screen.getByText('Each probe key can only be used by one route')
    ).toBeVisible()
    expect(api.put).not.toHaveBeenCalled()
  })

  it('keeps typed values and focus stable, then saves only the target IDs and numeric settings', async () => {
    const saved = {
      ...monitorResponse,
      data: {
        ...monitorResponse.data,
        targets: [
          { ...monitorResponse.data.targets[0], interval_seconds: 600 },
        ],
      },
    }
    let resolveSave!: (value: { data: typeof saved }) => void
    vi.mocked(api.get).mockResolvedValueOnce({ data: monitorResponse } as never)
    vi.mocked(api.put).mockReturnValueOnce(
      new Promise((resolve) => {
        resolveSave = resolve
      }) as never
    )
    const user = userEvent.setup()

    renderAdminPage()
    const input = await screen.findByRole('spinbutton', {
      name: 'Maximum idle interval (seconds) 1',
    })
    await user.clear(input)
    await user.type(input, '600')
    expect(input).toHaveFocus()
    expect(input).toHaveValue(600)
    await user.click(screen.getByRole('button', { name: 'Save settings' }))

    expect(
      await screen.findByRole('button', { name: 'Saving...' })
    ).toBeDisabled()
    expect(input).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Add route' })).toBeDisabled()
    expect(api.put).toHaveBeenCalledWith(
      '/api/admin/channel-status/targets',
      { targets: saved.data.targets },
      { skipBusinessError: true, skipErrorHandler: true }
    )

    resolveSave({ data: saved })
    expect(
      await screen.findByRole('button', { name: 'Save settings' })
    ).toBeDisabled()
    await waitFor(() =>
      expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument()
    )
    expect(
      screen.getByRole('spinbutton', {
        name: 'Maximum idle interval (seconds) 1',
      })
    ).toHaveValue(600)
    expect(
      screen.getByRole('button', { name: 'Refresh settings' })
    ).toBeEnabled()
  })

  it('keeps the draft and reports a failed business save so the same settings can be retried', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ data: monitorResponse } as never)
    vi.mocked(api.put).mockResolvedValueOnce({
      data: {
        success: false,
        message: 'The selected probe token is unavailable',
      },
    } as never)
    const user = userEvent.setup()

    renderAdminPage()
    const input = await screen.findByRole('spinbutton', {
      name: 'Maximum idle interval (seconds) 1',
    })
    await user.clear(input)
    await user.type(input, '600')
    await user.click(screen.getByRole('button', { name: 'Save settings' }))

    expect(
      await screen.findByText('The selected probe token is unavailable')
    ).toBeVisible()
    expect(input).toHaveValue(600)
    expect(screen.getByRole('button', { name: 'Save settings' })).toBeEnabled()
    expect(screen.getByText('Unsaved changes')).toBeVisible()
  })

  it('preserves a dirty draft after background refresh and requires confirmation to discard it', async () => {
    vi.mocked(api.get)
      .mockResolvedValueOnce({ data: monitorResponse } as never)
      .mockResolvedValueOnce({
        data: {
          ...monitorResponse,
          data: {
            ...monitorResponse.data,
            targets: [
              { ...monitorResponse.data.targets[0], interval_seconds: 900 },
            ],
          },
        },
      } as never)
      .mockResolvedValueOnce({
        data: {
          ...monitorResponse,
          data: {
            ...monitorResponse.data,
            targets: [
              { ...monitorResponse.data.targets[0], interval_seconds: 1200 },
            ],
          },
        },
      } as never)
    const user = userEvent.setup()
    const { queryClient } = renderAdminPage()
    const input = await screen.findByRole('spinbutton', {
      name: 'Maximum idle interval (seconds) 1',
    })
    await user.clear(input)
    await user.type(input, '600')
    await act(async () => {
      await queryClient.refetchQueries({
        queryKey: ['channel-status-monitor-settings'],
      })
    })
    expect(input).toHaveValue(600)

    await user.click(screen.getByRole('button', { name: 'Refresh settings' }))
    expect(await screen.findByRole('alertdialog')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(input).toHaveValue(600)
    expect(api.get).toHaveBeenCalledTimes(2)
    await user.click(screen.getByRole('button', { name: 'Refresh settings' }))
    await user.click(
      await screen.findByRole('button', { name: 'Discard changes' })
    )

    await waitFor(() =>
      expect(
        screen.getByRole('spinbutton', {
          name: 'Maximum idle interval (seconds) 1',
        })
      ).toHaveValue(1200)
    )
    expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument()
  })

  it('keeps the dirty draft visible when a background refresh fails', async () => {
    vi.mocked(api.get)
      .mockResolvedValueOnce({ data: monitorResponse } as never)
      .mockRejectedValueOnce(new Error('temporary outage'))
    const user = userEvent.setup()
    const { queryClient } = renderAdminPage()
    const input = await screen.findByRole('spinbutton', {
      name: 'Maximum idle interval (seconds) 1',
    })
    await user.clear(input)
    await user.type(input, '600')
    await act(async () => {
      await queryClient.refetchQueries({
        queryKey: ['channel-status-monitor-settings'],
      })
    })

    expect(
      await screen.findByText(
        'Refresh failed. Your current settings and unsaved changes are preserved.'
      )
    ).toBeVisible()
    expect(input).toHaveValue(600)
    expect(screen.getByRole('button', { name: 'Save settings' })).toBeEnabled()
  })

  it('keeps the saved settings when an older background GET resolves after the save', async () => {
    const saved = {
      ...monitorResponse,
      data: {
        ...monitorResponse.data,
        targets: [
          { ...monitorResponse.data.targets[0], interval_seconds: 600 },
        ],
      },
    }
    let resolveSave!: (value: { data: typeof saved }) => void
    let resolveRefresh!: (value: { data: typeof monitorResponse }) => void
    vi.mocked(api.get)
      .mockResolvedValueOnce({ data: monitorResponse } as never)
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveRefresh = resolve
        }) as never
      )
    vi.mocked(api.put).mockReturnValueOnce(
      new Promise((resolve) => {
        resolveSave = resolve
      }) as never
    )
    const user = userEvent.setup()
    const { queryClient } = renderAdminPage()
    const input = await screen.findByRole('spinbutton', {
      name: 'Maximum idle interval (seconds) 1',
    })
    await user.clear(input)
    await user.type(input, '600')
    await user.click(screen.getByRole('button', { name: 'Save settings' }))
    await screen.findByRole('button', { name: 'Saving...' })
    let refreshRequest!: Promise<void>
    await act(async () => {
      refreshRequest = queryClient.refetchQueries({
        queryKey: ['channel-status-monitor-settings'],
      })
    })
    expect(api.get).toHaveBeenCalledTimes(2)
    await act(async () => {
      resolveSave({ data: saved })
    })
    await screen.findByRole('button', { name: 'Save settings' })
    await act(async () => {
      resolveRefresh({ data: monitorResponse })
      await refreshRequest
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Refresh settings' })
      ).toBeEnabled()
    )

    expect(
      screen.getByRole('spinbutton', {
        name: 'Maximum idle interval (seconds) 1',
      })
    ).toHaveValue(600)
    expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument()
  })

  it('shows the missing-key guidance and rejects saving when the selected route has no eligible key', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...monitorResponse,
        data: { ...monitorResponse.data, probe_tokens: [] },
      },
    } as never)
    const user = userEvent.setup()
    renderAdminPage()
    const input = await screen.findByRole('spinbutton', {
      name: 'Maximum idle interval (seconds) 1',
    })
    expect(screen.getByRole('combobox', { name: 'Probe key 1' })).toBeDisabled()
    expect(screen.getByRole('link', { name: 'API Keys' })).toHaveAttribute(
      'href',
      '/keys'
    )
    await user.clear(input)
    await user.type(input, '600')
    await user.click(screen.getByRole('button', { name: 'Save settings' }))

    expect(
      await screen.findByText('Select an eligible probe key')
    ).toBeVisible()
    expect(api.put).not.toHaveBeenCalled()
  })

  it('allows intentionally removing the final route and saving an empty list', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ data: monitorResponse } as never)
    vi.mocked(api.put).mockResolvedValueOnce({
      data: {
        ...monitorResponse,
        data: { ...monitorResponse.data, targets: [] },
      },
    } as never)
    const user = userEvent.setup()
    renderAdminPage()
    await user.click(
      await screen.findByRole('button', { name: 'Remove route 1' })
    )
    await user.click(screen.getByRole('button', { name: 'Save settings' }))

    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith(
        '/api/admin/channel-status/targets',
        { targets: [] },
        { skipBusinessError: true, skipErrorHandler: true }
      )
    )
    expect(await screen.findByText('No monitored routes')).toBeVisible()
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save settings' })
      ).toBeDisabled()
    )
  })

  it('disables adding more than 100 routes and re-enables it after one is removed', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...monitorResponse,
        data: {
          ...monitorResponse.data,
          targets: Array.from({ length: 100 }, () => ({
            ...monitorResponse.data.targets[0],
          })),
        },
      },
    } as never)
    const user = userEvent.setup()
    renderAdminPage()

    expect(
      await screen.findByRole('button', { name: 'Remove route 100' })
    ).toBeVisible()
    expect(screen.getByRole('button', { name: 'Add route' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Remove route 100' }))
    expect(screen.getByRole('button', { name: 'Add route' })).toBeEnabled()
  })
})

afterEach(() => {
  cleanup()
  api.defaults.adapter = originalAdapter
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('channel status page', () => {
  it('shows all monitored routes together and supports searching without status filters', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...statusResponse,
        data: {
          ...statusResponse.data,
          targets: [
            statusResponse.data.targets[0],
            {
              ...statusResponse.data.targets[0],
              group: 'premium',
              model: 'slow-model',
              status: 'error',
            },
          ],
        },
      },
    } as never)
    const user = userEvent.setup()
    renderPage()
    expect(await screen.findByText('slow-model')).toBeVisible()
    await user.type(
      screen.getByRole('searchbox', { name: 'Search groups or models' }),
      'mhenwa'
    )
    expect(screen.getByText('mhenwa-chat')).toBeVisible()
    expect(screen.queryByText('slow-model')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('combobox', { name: 'Filter by status' })
    ).not.toBeInTheDocument()
    expect(
      within(
        screen.getByRole('region', { name: 'Status summary' })
      ).queryByRole('button')
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Reset filters' }))
    expect(screen.getByText('slow-model')).toBeVisible()
    expect(screen.getByText('mhenwa-chat')).toBeVisible()
  })

  it('switches from monitoring cards to the existing accessible table view', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ data: statusResponse } as never)
    const user = userEvent.setup()
    renderPage()
    expect(
      await screen.findByRole('article', { name: 'standard / mhenwa-chat' })
    ).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Table view' }))
    expect(screen.getByRole('table')).toBeVisible()
    expect(screen.queryByRole('article')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Card view' }))
    expect(
      screen.getByRole('article', { name: 'standard / mhenwa-chat' })
    ).toBeVisible()
  })

  it('keeps zero effective rates visible and does not render output speed as a primary metric', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...statusResponse,
        data: {
          ...statusResponse.data,
          targets: [{ ...statusResponse.data.targets[0], effective_ratio: 0 }],
        },
      },
    } as never)
    renderPage()
    const route = await screen.findByRole('article', {
      name: 'standard / mhenwa-chat',
    })
    expect(within(route).getByText('×0')).toBeVisible()
    expect(within(route).queryByText('Output speed')).not.toBeInTheDocument()
    expect(within(route).getByText('420 ms')).toBeVisible()
  })

  it('renders missing history as unknown without inventing a successful availability rate', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ data: statusResponse } as never)
    renderPage()
    const route = await screen.findByRole('article', {
      name: 'standard / mhenwa-chat',
    })
    expect(
      within(route).getByText('History collection has not started.')
    ).toBeVisible()
    expect(within(route).getByText('0 observations')).toBeVisible()
    expect(within(route).queryByText('100%')).not.toBeInTheDocument()
    const bars = within(route).getAllByRole('group', {
      name: 'Status history',
    })[0]
    expect(within(bars).getAllByRole('button')).toHaveLength(60)
    expect(
      within(bars)
        .getAllByRole('button')
        .filter((button) => button.tabIndex === 0)
    ).toHaveLength(1)
  })

  it('keeps completed samples with missing latency yellow and explains the missing metrics', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...statusResponse,
        data: {
          ...statusResponse.data,
          targets: [
            {
              ...statusResponse.data.targets[0],
              status: 'degraded',
              ttft_ms: undefined,
              latency_ms: undefined,
            },
          ],
        },
      },
    } as never)
    renderPage()
    const route = await screen.findByRole('article', {
      name: 'standard / mhenwa-chat',
    })
    expect(
      within(route).getByTitle('Latency data is incomplete or latency is high.')
    ).toHaveTextContent('High latency')
    expect(within(route).getByText('Real request')).toBeVisible()
    expect(within(route).queryByText('Probing')).not.toBeInTheDocument()
    expect(within(route).queryByText('No data')).not.toBeInTheDocument()
    expect(screen.getByText('Some routes need attention')).toBeVisible()
  })

  it('shows empty history without a success percentage even if an empty response includes a rate', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...statusResponse,
        data: {
          ...statusResponse.data,
          targets: [
            {
              ...statusResponse.data.targets[0],
              history: {
                observation_limit: 60,
                real_sample_limit: 30,
                observations: [],
                real_samples: [],
                success_rate: 100,
              },
            },
          ],
        },
      },
    } as never)
    renderPage()
    const route = await screen.findByRole('article', {
      name: 'standard / mhenwa-chat',
    })
    expect(within(route).getByText('No observations yet.')).toBeVisible()
    expect(within(route).queryByText('100%')).not.toBeInTheDocument()
    expect(within(route).getByText('0 observations')).toBeVisible()
  })

  it('refreshes data every 30 seconds without resetting route filtering', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    vi.mocked(api.get).mockResolvedValue({ data: statusResponse } as never)
    const user = userEvent.setup()
    const page = renderPage()
    await screen.findByText('mhenwa-chat')
    await user.type(
      screen.getByRole('searchbox', { name: 'Search groups or models' }),
      'mhenwa'
    )
    expect(
      screen.queryByRole('combobox', { name: 'History window' })
    ).not.toBeInTheDocument()
    expect(screen.getByText('Auto-refreshing every 30s')).toBeVisible()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(29_999)
    })
    expect(api.get).toHaveBeenCalledTimes(1)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1)
    })
    await waitFor(() => expect(api.get).toHaveBeenCalledTimes(2))
    expect(api.get).toHaveBeenLastCalledWith('/api/channel-status', {
      signal: expect.any(AbortSignal),
      disableDuplicate: true,
    })
    expect(
      screen.getByRole('searchbox', { name: 'Search groups or models' })
    ).toHaveValue('mhenwa')
    page.unmount()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000)
    })
    expect(api.get).toHaveBeenCalledTimes(2)
    vi.useRealTimers()
  })

  it('rejects invalid history and negative effective rates while preserving valid current status', async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      data: {
        ...statusResponse,
        data: {
          ...statusResponse.data,
          targets: [
            {
              ...statusResponse.data.targets[0],
              effective_ratio: -2,
              history: { window_seconds: -1, buckets: [] },
            },
          ],
        },
      },
    } as never)
    renderPage()
    const route = await screen.findByRole('article', {
      name: 'standard / mhenwa-chat',
    })
    expect(
      within(route).getByText('History collection has not started.')
    ).toBeVisible()
    expect(within(route).queryByText('×-2')).not.toBeInTheDocument()
    expect(within(route).getByText('Healthy')).toBeVisible()
  })

  it('loads public status through the real HTTP client when StrictMode cancels and restarts its first request', async () => {
    api.defaults.adapter = vi.fn<AxiosAdapter>(async (config) => ({
      data: statusResponse,
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }))

    renderPage(true)

    expect(await screen.findByText('mhenwa-chat')).toBeVisible()
    expect(api.get).toHaveBeenCalledTimes(2)
    expect(vi.mocked(api.get).mock.calls[0][1]?.signal?.aborted).toBe(true)
    expect(vi.mocked(api.get).mock.calls[1][1]?.signal?.aborted).toBe(false)
    expect(
      screen.queryByText('Unable to load service status')
    ).not.toBeInTheDocument()
  })

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
    expect(screen.getByText('×0.035')).toBeVisible()
    expect(screen.queryByText(/cache rate/i)).not.toBeInTheDocument()
    expect(api.get).toHaveBeenCalledWith('/api/channel-status', {
      signal: expect.any(AbortSignal),
      disableDuplicate: true,
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
              status: 'future_unknown',
              source: 'synthetic',
            },
          ],
        },
      },
    } as never)

    renderPage()

    const route = await screen.findByRole('article', {
      name: 'standard / mhenwa-chat',
    })
    expect(within(route).getByText('No data')).toBeVisible()
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
