import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Plus, RefreshCw, Save, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  getChannelStatusMonitor,
  updateChannelStatusMonitor,
  type ChannelStatusMonitorData,
  type ChannelStatusMonitorTarget,
} from './admin-api'

const queryKey = ['channel-status-monitor-settings'] as const

const emptyTarget = (
  data?: ChannelStatusMonitorData
): ChannelStatusMonitorTarget => {
  const group = data?.groups[0] ?? ''
  const model = data?.models_by_group[group]?.[0] ?? ''
  const token = data?.probe_tokens.find((item) => item.group === group)
  return {
    group,
    model,
    probe_token_id: token?.id ?? 0,
    interval_seconds: 300,
    timeout_seconds: 30,
  }
}

function tokenLabel(token: { id: number; name?: string; user_id?: number }) {
  const label = token.name?.trim() || `#${token.id}`
  return token.user_id ? `${label} (#${token.user_id})` : label
}

export function ChannelStatusAdminPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey,
    queryFn: async () => requireServerSuccess(await getChannelStatusMonitor()),
    staleTime: 15_000,
  })
  const data = query.data?.data
  const [draftTargets, setDraftTargets] = useState<
    ChannelStatusMonitorTarget[] | null
  >(null)
  const targets =
    draftTargets ?? data?.targets.map((target) => ({ ...target })) ?? []

  const saveMutation = useMutation({
    mutationFn: () => updateChannelStatusMonitor(targets),
    onSuccess: async (response) => {
      const result = requireServerSuccess(response)
      if (result.data) {
        setTargets(result.data.targets.map((target) => ({ ...target })))
      }
      await queryClient.invalidateQueries({ queryKey })
      toast.success(t('Channel status monitor settings saved'))
    },
    onError: (error) =>
      handleServerError(error, t('Failed to save channel status settings')),
  })

  const groups = data?.groups ?? []
  const modelsByGroup = data?.models_by_group ?? {}
  const probeTokens = data?.probe_tokens ?? []
  let sourceLabel = t('No saved configuration')
  if (data?.source === 'database') {
    sourceLabel = t('Database configuration')
  } else if (data?.source === 'environment') {
    sourceLabel = t('Environment fallback (save to take ownership)')
  }

  const updateTarget = (
    index: number,
    patch: Partial<ChannelStatusMonitorTarget>
  ) => {
    setDraftTargets((current) =>
      (current ?? data?.targets ?? []).map((target, targetIndex) =>
        targetIndex === index ? { ...target, ...patch } : target
      )
    )
  }

  const setTargets = (
    next:
      | ChannelStatusMonitorTarget[]
      | ((current: ChannelStatusMonitorTarget[]) => ChannelStatusMonitorTarget[])
  ) => {
    setDraftTargets((current) => {
      const base = current ?? data?.targets ?? []
      return typeof next === 'function' ? next(base) : next
    })
  }

  const addTarget = () => {
    if (targets.length >= 100) {
      toast.error(t('You can configure at most 100 monitored routes'))
      return
    }
    setDraftTargets((current) => [...(current ?? data?.targets ?? []), emptyTarget(data)])
  }

  const modelOptions = (group: string) => modelsByGroup[group] ?? []

  const tokenOptions = (target: ChannelStatusMonitorTarget) =>
    probeTokens.filter((token) => {
      if (token.group !== target.group) return false
      if (!token.models || token.models.length === 0) return true
      return token.models.includes(target.model)
    })

  let routeContent
  if (query.isLoading) {
    routeContent = (
      <div className='text-muted-foreground rounded-md border p-6 text-sm'>
        {t('Loading settings...')}
      </div>
    )
  } else if (query.isError) {
    routeContent = (
      <div className='text-destructive border-destructive/30 rounded-md border p-6 text-sm'>
        {t('Unable to load channel status settings.')}
      </div>
    )
  } else if (targets.length === 0) {
    routeContent = (
      <div className='text-muted-foreground rounded-md border p-6 text-sm'>
        {t(
          'No monitored routes configured. Add a group and model to enable active probes.'
        )}
      </div>
    )
  } else {
    routeContent = (
      <div className='overflow-x-auto rounded-md border'>
        <table className='w-full min-w-[900px] text-sm'>
          <thead className='bg-muted/40 border-b'>
            <tr>
              <th className='px-3 py-2 text-left font-medium'>
                {t('Group')}
              </th>
              <th className='px-3 py-2 text-left font-medium'>
                {t('Model')}
              </th>
              <th className='px-3 py-2 text-left font-medium'>
                {t('Probe key')}
              </th>
              <th className='w-36 px-3 py-2 text-left font-medium'>
                {t('Interval (seconds)')}
              </th>
              <th className='w-36 px-3 py-2 text-left font-medium'>
                {t('Timeout (seconds)')}
              </th>
              <th className='w-16 px-3 py-2' />
            </tr>
          </thead>
          <tbody>
            {targets.map((target, index) => {
              const models = modelOptions(target.group)
              const tokens = tokenOptions(target)
              return (
                <tr
                  key={`${target.group}:${target.model}:${target.probe_token_id}:${target.interval_seconds}:${target.timeout_seconds}`}
                  className='border-b last:border-0'
                >
                  <td className='p-2'>
                    <Select
                      value={target.group}
                      onValueChange={(value) => {
                        const nextGroup = value ?? ''
                        const nextModel = modelsByGroup[nextGroup]?.[0] ?? ''
                        const nextToken = probeTokens.find(
                          (item) => item.group === nextGroup
                        )
                        updateTarget(index, {
                          group: nextGroup,
                          model: nextModel,
                          probe_token_id: nextToken?.id ?? 0,
                        })
                      }}
                    >
                      <SelectTrigger className='w-full'>
                        <SelectValue placeholder={t('Select group')} />
                      </SelectTrigger>
                      <SelectContent>
                        {groups.map((group) => (
                          <SelectItem key={group} value={group}>
                            {group}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </td>
                  <td className='p-2'>
                    <Select
                      value={target.model}
                      onValueChange={(value) =>
                        updateTarget(index, { model: value ?? '' })
                      }
                    >
                      <SelectTrigger className='w-full'>
                        <SelectValue placeholder={t('Select model')} />
                      </SelectTrigger>
                      <SelectContent>
                        {models.map((model) => (
                          <SelectItem key={model} value={model}>
                            {model}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </td>
                  <td className='p-2'>
                    <Select
                      value={
                        target.probe_token_id > 0
                          ? String(target.probe_token_id)
                          : ''
                      }
                      onValueChange={(value) =>
                        updateTarget(index, {
                          probe_token_id: Number(value ?? 0),
                        })
                      }
                    >
                      <SelectTrigger className='w-full'>
                        <SelectValue placeholder={t('Select probe key')} />
                      </SelectTrigger>
                      <SelectContent>
                        {tokens.map((token) => (
                          <SelectItem
                            key={token.id}
                            value={String(token.id)}
                          >
                            {tokenLabel(token)}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    {tokens.length === 0 && (
                      <p className='text-destructive mt-1 text-xs'>
                        {t(
                          'No eligible key for this group/model. Create one in'
                        )}{' '}
                        <Link className='underline' to='/keys'>
                          {t('API Keys')}
                        </Link>
                        .
                      </p>
                    )}
                  </td>
                  <td className='p-2'>
                    <Input
                      type='number'
                      min={60}
                      max={3600}
                      value={target.interval_seconds}
                      onChange={(event) =>
                        updateTarget(index, {
                          interval_seconds: Number(event.target.value),
                        })
                      }
                    />
                  </td>
                  <td className='p-2'>
                    <Input
                      type='number'
                      min={5}
                      max={120}
                      value={target.timeout_seconds}
                      onChange={(event) =>
                        updateTarget(index, {
                          timeout_seconds: Number(event.target.value),
                        })
                      }
                    />
                  </td>
                  <td className='p-2 text-right'>
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon'
                      aria-label={t('Remove route')}
                      title={t('Remove route')}
                      onClick={() =>
                        setTargets((current) =>
                          current.filter(
                            (_, targetIndex) => targetIndex !== index
                          )
                        )
                      }
                    >
                      <Trash2 className='text-destructive' />
                    </Button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    )
  }

  return (
      current.map((target, targetIndex) =>
        targetIndex === index ? { ...target, ...patch } : target
      )
    )
  }

  const addTarget = () => {
    if (targets.length >= 100) {
      toast.error(t('You can configure at most 100 monitored routes'))
      return
    }
    setTargets((current) => [...current, emptyTarget(data)])
  }

  const modelOptions = (group: string) => modelsByGroup[group] ?? []

  const tokenOptions = (target: ChannelStatusMonitorTarget) =>
    probeTokens.filter((token) => {
      if (token.group !== target.group) return false
      if (!token.models || token.models.length === 0) return true
      return token.models.includes(target.model)
    })

  let routeContent
  if (query.isLoading) {
    routeContent = (
      <div className='text-muted-foreground rounded-md border p-6 text-sm'>
        {t('Loading settings...')}
      </div>
    )
  } else if (query.isError) {
    routeContent = (
      <div className='text-destructive border-destructive/30 rounded-md border p-6 text-sm'>
        {t('Unable to load channel status settings.')}
      </div>
    )
  } else if (targets.length === 0) {
    routeContent = (
      <div className='text-muted-foreground rounded-md border p-6 text-sm'>
        {t(
          'No monitored routes configured. Add a group and model to enable active probes.'
        )}
      </div>
    )
  } else {
    routeContent = (
      <div className='overflow-x-auto rounded-md border'>
        <table className='w-full min-w-[900px] text-sm'>
          <thead className='bg-muted/40 border-b'>
            <tr>
              <th className='px-3 py-2 text-left font-medium'>
                {t('Group')}
              </th>
              <th className='px-3 py-2 text-left font-medium'>
                {t('Model')}
              </th>
              <th className='px-3 py-2 text-left font-medium'>
                {t('Probe key')}
              </th>
              <th className='w-36 px-3 py-2 text-left font-medium'>
                {t('Interval (seconds)')}
              </th>
              <th className='w-36 px-3 py-2 text-left font-medium'>
                {t('Timeout (seconds)')}
              </th>
              <th className='w-16 px-3 py-2' />
            </tr>
          </thead>
          <tbody>
            {targets.map((target, index) => {
              const models = modelOptions(target.group)
              const tokens = tokenOptions(target)
              return (
                <tr
                  key={`${target.group}:${target.model}:${target.probe_token_id}`}
                  className='border-b last:border-0'
                >
                  <td className='p-2'>
                    <Select
                      value={target.group}
                      onValueChange={(value) => {
                        const nextGroup = value ?? ''
                        const nextModel = modelsByGroup[nextGroup]?.[0] ?? ''
                        const nextToken = probeTokens.find(
                          (item) => item.group === nextGroup
                        )
                        updateTarget(index, {
                          group: nextGroup,
                          model: nextModel,
                          probe_token_id: nextToken?.id ?? 0,
                        })
                      }}
                    >
                      <SelectTrigger className='w-full'>
                        <SelectValue placeholder={t('Select group')} />
                      </SelectTrigger>
                      <SelectContent>
                        {groups.map((group) => (
                          <SelectItem key={group} value={group}>
                            {group}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </td>
                  <td className='p-2'>
                    <Select
                      value={target.model}
                      onValueChange={(value) =>
                        updateTarget(index, { model: value ?? '' })
                      }
                    >
                      <SelectTrigger className='w-full'>
                        <SelectValue placeholder={t('Select model')} />
                      </SelectTrigger>
                      <SelectContent>
                        {models.map((model) => (
                          <SelectItem key={model} value={model}>
                            {model}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </td>
                  <td className='p-2'>
                    <Select
                      value={
                        target.probe_token_id > 0
                          ? String(target.probe_token_id)
                          : ''
                      }
                      onValueChange={(value) =>
                        updateTarget(index, {
                          probe_token_id: Number(value ?? 0),
                        })
                      }
                    >
                      <SelectTrigger className='w-full'>
                        <SelectValue placeholder={t('Select probe key')} />
                      </SelectTrigger>
                      <SelectContent>
                        {tokens.map((token) => (
                          <SelectItem
                            key={token.id}
                            value={String(token.id)}
                          >
                            {tokenLabel(token)}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    {tokens.length === 0 && (
                      <p className='text-destructive mt-1 text-xs'>
                        {t(
                          'No eligible key for this group/model. Create one in'
                        )}{' '}
                        <Link className='underline' to='/keys'>
                          {t('API Keys')}
                        </Link>
                        .
                      </p>
                    )}
                  </td>
                  <td className='p-2'>
                    <Input
                      type='number'
                      min={60}
                      max={3600}
                      value={target.interval_seconds}
                      onChange={(event) =>
                        updateTarget(index, {
                          interval_seconds: Number(event.target.value),
                        })
                      }
                    />
                  </td>
                  <td className='p-2'>
                    <Input
                      type='number'
                      min={5}
                      max={120}
                      value={target.timeout_seconds}
                      onChange={(event) =>
                        updateTarget(index, {
                          timeout_seconds: Number(event.target.value),
                        })
                      }
                    />
                  </td>
                  <td className='p-2 text-right'>
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon'
                      aria-label={t('Remove route')}
                      title={t('Remove route')}
                      onClick={() =>
                        setTargets((current) =>
                          current.filter(
                            (_, targetIndex) => targetIndex !== index
                          )
                        )
                      }
                    >
                      <Trash2 className='text-destructive' />
                    </Button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Channel status settings')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <span className='text-muted-foreground hidden text-xs sm:inline'>
          {sourceLabel}
        </span>
        <Button
          type='button'
          variant='outline'
          size='icon'
          aria-label={t('Refresh settings')}
          title={t('Refresh settings')}
          onClick={() => void query.refetch()}
          disabled={query.isFetching || saveMutation.isPending}
        >
          <RefreshCw
            className={query.isFetching ? 'animate-spin' : undefined}
          />
        </Button>
        <Button
          type='button'
          onClick={() => saveMutation.mutate()}
          disabled={saveMutation.isPending || query.isLoading}
        >
          <Save data-icon='inline-start' />
          {saveMutation.isPending ? t('Saving...') : t('Save settings')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <div className='bg-muted/40 text-muted-foreground rounded-md border px-3 py-2 text-sm'>
            {t(
              'Each route uses one finite, loopback-only probe key. Changes take effect on all cluster instances without restarting the containers.'
            )}
          </div>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <div>
              <h3 className='font-medium'>
                {t('Monitored group and model routes')}
              </h3>
              <p className='text-muted-foreground text-sm'>
                {t('Real user requests take priority over scheduled probes.')}
              </p>
            </div>
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={addTarget}
              disabled={query.isLoading}
            >
              <Plus data-icon='inline-start' />
              {t('Add route')}
            </Button>
          </div>
          {routeContent}
          {probeTokens.length === 0 && (
            <p className='text-destructive text-sm'>
              {t(
                'No eligible probe keys are available. Create a non-admin key with a finite quota, one group, one model, and a 127.0.0.1 IP restriction before enabling probes.'
              )}
            </p>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
