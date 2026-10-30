import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Plus, RefreshCw, Save } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useFieldArray, useForm, FormProvider } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { FieldError, FieldGroup } from '@/components/ui/field'
import {
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import {
  getChannelStatusMonitor,
  updateChannelStatusMonitor,
  type ChannelStatusMonitorTarget,
} from './admin-api'
import { MonitorTargetRow } from './components/monitor-target-row'
import {
  createMonitorFormSchema,
  eligibleProbeTokens,
  monitorDataSchema,
  type MonitorFormValues,
} from './lib/admin-schema'

const queryKey = ['channel-status-monitor-settings'] as const

export function ChannelStatusAdminPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [confirmRefresh, setConfirmRefresh] = useState(false)
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) =>
      monitorDataSchema.parse(
        requireServerSuccess(await getChannelStatusMonitor(signal)).data
      ),
    staleTime: 15_000,
    refetchOnWindowFocus: false,
  })
  const data = query.data
  const schema = useMemo(() => createMonitorFormSchema(data, t), [data, t])
  const form = useForm<MonitorFormValues>({
    resolver: zodResolver(schema),
    defaultValues: { targets: [] },
  })
  const rows = useFieldArray({ control: form.control, name: 'targets' })
  const { isDirty } = form.formState
  const { reset } = form

  useEffect(() => {
    if (data && !isDirty) reset({ targets: data.targets })
  }, [data, isDirty, reset])

  const saveMutation = useMutation({
    mutationFn: async (values: MonitorFormValues) =>
      monitorDataSchema.parse(
        requireServerSuccess(await updateChannelStatusMonitor(values.targets))
          .data
      ),
    onSuccess: async (savedData) => {
      await queryClient.cancelQueries({ queryKey })
      queryClient.setQueryData(queryKey, savedData)
      reset({ targets: savedData.targets })
      toast.success(t('Channel status monitor settings saved'))
      await queryClient.invalidateQueries({ queryKey: ['channel-status'] })
    },
    onError: (error) =>
      handleServerError(error, t('Failed to save channel status settings')),
  })
  const disabled = !data || saveMutation.isPending

  const refresh = () => {
    if (data) reset({ targets: data.targets })
    setConfirmRefresh(false)
    void query.refetch()
  }

  const addTarget = () => {
    if (!data || rows.fields.length >= 100) return
    const targets = form.getValues('targets')
    let nextTarget: ChannelStatusMonitorTarget = {
      group: data.groups[0] ?? '',
      model: '',
      probe_token_id: 0,
      interval_seconds: 300,
      timeout_seconds: 30,
    }
    for (const group of data.groups) {
      const model = data.models_by_group[group]?.find(
        (name) =>
          !targets.some(
            (target) => target.group === group && target.model === name
          )
      )
      if (!model) continue
      const token = eligibleProbeTokens(data.probe_tokens, {
        group,
        model,
      }).find(
        (item) => !targets.some((target) => target.probe_token_id === item.id)
      )
      nextTarget = {
        ...nextTarget,
        group,
        model,
        probe_token_id: token?.id ?? 0,
      }
      if (token) break
    }
    rows.append(nextTarget, { shouldFocus: false })
  }

  let sourceLabel = t('No saved configuration')
  if (data?.source === 'database') sourceLabel = t('Database configuration')
  if (data?.source === 'environment') {
    sourceLabel = t('Environment fallback (save to take ownership)')
  }

  let routeContent = <LoadingState message={t('Loading settings...')} />
  if (query.isError && !data) {
    routeContent = (
      <ErrorState
        title={t('Unable to load channel status settings.')}
        description={t('Check your connection and try again.')}
        onRetry={refresh}
      />
    )
  } else if (data && rows.fields.length === 0) {
    routeContent = (
      <EmptyState
        icon={Activity}
        title={t('No monitored routes')}
        description={t(
          'No monitored routes configured. Add a group and model to enable active probes.'
        )}
        bordered
      />
    )
  } else if (data) {
    routeContent = (
      <StaticDataTable
        tableClassName='min-w-[900px]'
        tableProps={{ 'aria-label': t('Monitored group and model routes') }}
      >
        <TableHeader>
          <TableRow>
            <TableHead>{t('Group')}</TableHead>
            <TableHead>{t('Model')}</TableHead>
            <TableHead>{t('Probe key')}</TableHead>
            <TableHead className='w-40'>
              {t('Maximum idle interval (seconds)')}
            </TableHead>
            <TableHead className='w-40'>{t('Timeout (seconds)')}</TableHead>
            <TableHead className='w-12'>
              <span className='sr-only'>{t('Remove route')}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.fields.map((field, index) => (
            <MonitorTargetRow
              key={field.id}
              index={index}
              data={data}
              disabled={disabled}
              onRemove={() => rows.remove(index)}
            />
          ))}
        </TableBody>
      </StaticDataTable>
    )
  }

  return (
    <FormProvider {...form}>
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
            onClick={() => (isDirty ? setConfirmRefresh(true) : refresh())}
            disabled={query.isFetching || saveMutation.isPending}
          >
            <RefreshCw
              className={query.isFetching ? 'animate-spin' : undefined}
              aria-hidden='true'
            />
          </Button>
          <Button
            type='submit'
            form='channel-status-monitor-form'
            disabled={
              disabled ||
              query.isFetching ||
              (!isDirty && data?.source !== 'environment')
            }
          >
            <Save data-icon='inline-start' aria-hidden='true' />
            {saveMutation.isPending ? t('Saving...') : t('Save settings')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <form
            id='channel-status-monitor-form'
            noValidate
            onSubmit={form.handleSubmit((values) => {
              if (data) saveMutation.mutate(values)
            })}
          >
            <FieldGroup>
              <Alert>
                <AlertDescription>
                  {t(
                    'Each route uses one finite, loopback-only probe key. Changes take effect on all cluster instances without restarting the containers.'
                  )}
                </AlertDescription>
              </Alert>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <div>
                  <h3 className='font-medium'>
                    {t('Monitored group and model routes')}
                  </h3>
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'A fallback probe runs after the maximum idle interval without a completed real request or probe, provided no real request is active.'
                    )}
                  </p>
                </div>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  onClick={addTarget}
                  disabled={disabled || rows.fields.length >= 100}
                >
                  <Plus data-icon='inline-start' aria-hidden='true' />
                  {t('Add route')}
                </Button>
              </div>
              {isDirty && (
                <p className='text-muted-foreground text-sm' role='status'>
                  {t('Unsaved changes')}
                </p>
              )}
              {query.isError && data && (
                <Alert variant='destructive'>
                  <AlertDescription>
                    {t(
                      'Refresh failed. Your current settings and unsaved changes are preserved.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
              {saveMutation.isError && (
                <Alert variant='destructive'>
                  <AlertDescription>
                    {getServerErrorMessage(
                      saveMutation.error,
                      t('Failed to save channel status settings')
                    )}
                  </AlertDescription>
                </Alert>
              )}
              <FieldError>
                {form.formState.errors.targets?.root?.message}
              </FieldError>
              {routeContent}
              {data && rows.fields.length >= 100 && (
                <p className='text-muted-foreground text-sm'>
                  {t('You can configure at most 100 monitored routes')}
                </p>
              )}
              {data && data.probe_tokens.length === 0 && (
                <Alert variant='destructive'>
                  <AlertDescription>
                    {t(
                      'No eligible probe keys are available. Create a non-admin key with a finite quota, one group, one model, and a 127.0.0.1 IP restriction before enabling probes.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
            </FieldGroup>
          </form>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <ConfirmDialog
        open={confirmRefresh}
        onOpenChange={setConfirmRefresh}
        title={t('Discard changes')}
        desc={t('Refreshing will discard your unsaved changes. Continue?')}
        confirmText={t('Discard changes')}
        handleConfirm={refresh}
      />
    </FormProvider>
  )
}
