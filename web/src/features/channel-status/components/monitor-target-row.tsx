import { Link } from '@tanstack/react-router'
import { Trash2 } from 'lucide-react'
import { Controller, useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { TableCell, TableRow } from '@/components/ui/table'
import { useMediaQuery } from '@/hooks/use-media-query'

import type { ChannelStatusMonitorData } from '../admin-api'
import {
  eligibleProbeTokens,
  type MonitorFormValues,
} from '../lib/admin-schema'

export function MonitorTargetRow(props: {
  index: number
  data: ChannelStatusMonitorData
  disabled: boolean
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const form = useFormContext<MonitorFormValues>()
  const target = useWatch({
    control: form.control,
    name: `targets.${props.index}`,
  })
  const models = props.data.models_by_group[target.group] ?? []
  const tokens = eligibleProbeTokens(props.data.probe_tokens, target)
  const selectedToken = tokens.find(
    (token) => token.id === target.probe_token_id
  )
  const errors = form.formState.errors.targets?.[props.index]

  const changeRoute = (group: string, model: string) => {
    const nextTokens = eligibleProbeTokens(props.data.probe_tokens, {
      group,
      model,
    })
    const token =
      nextTokens.find((item) => item.id === target.probe_token_id) ??
      nextTokens[0]
    form.setValue(`targets.${props.index}.group`, group, { shouldDirty: true })
    form.setValue(`targets.${props.index}.model`, model, { shouldDirty: true })
    form.setValue(`targets.${props.index}.probe_token_id`, token?.id ?? 0, {
      shouldDirty: true,
    })
    if (form.formState.isSubmitted) void form.trigger('targets')
  }

  return (
    <TableRow>
      <TableCell className='align-top'>
        <Field
          data-invalid={Boolean(errors?.group)}
          data-disabled={props.disabled}
        >
          <FieldLabel
            className='sr-only'
            htmlFor={`monitor-group-${props.index}`}
          >
            {t('Group')} {props.index + 1}
          </FieldLabel>
          <Controller
            control={form.control}
            name={`targets.${props.index}.group`}
            render={({ field }) =>
              isMobile ? (
                <NativeSelect
                  id={`monitor-group-${props.index}`}
                  value={
                    props.data.groups.includes(field.value) ? field.value : ''
                  }
                  disabled={props.disabled}
                  onBlur={field.onBlur}
                  aria-invalid={Boolean(errors?.group)}
                  className='w-full'
                  onChange={(event) => {
                    const group = event.target.value
                    changeRoute(
                      group,
                      props.data.models_by_group[group]?.[0] ?? ''
                    )
                  }}
                >
                  <NativeSelectOption value='' disabled>
                    {t('Select group')}
                  </NativeSelectOption>
                  {props.data.groups.map((group) => (
                    <NativeSelectOption key={group} value={group}>
                      {group}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              ) : (
                <Select
                  value={field.value}
                  disabled={props.disabled}
                  onValueChange={(value) => {
                    if (value === null) return
                    const group = value
                    changeRoute(
                      group,
                      props.data.models_by_group[group]?.[0] ?? ''
                    )
                  }}
                >
                  <SelectTrigger
                    id={`monitor-group-${props.index}`}
                    onBlur={field.onBlur}
                    aria-invalid={Boolean(errors?.group)}
                    className='w-full'
                  >
                    <SelectValue placeholder={t('Select group')}>
                      {target.group || t('Select group')}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {props.data.groups.map((group) => (
                        <SelectItem key={group} value={group}>
                          {group}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              )
            }
          />
          <FieldError>{errors?.group?.message}</FieldError>
        </Field>
      </TableCell>
      <TableCell className='align-top'>
        <Field
          data-invalid={Boolean(errors?.model)}
          data-disabled={props.disabled}
        >
          <FieldLabel
            className='sr-only'
            htmlFor={`monitor-model-${props.index}`}
          >
            {t('Model')} {props.index + 1}
          </FieldLabel>
          <Controller
            control={form.control}
            name={`targets.${props.index}.model`}
            render={({ field }) =>
              isMobile ? (
                <NativeSelect
                  id={`monitor-model-${props.index}`}
                  value={models.includes(field.value) ? field.value : ''}
                  disabled={props.disabled || models.length === 0}
                  onBlur={field.onBlur}
                  aria-invalid={Boolean(errors?.model)}
                  className='w-full'
                  onChange={(event) =>
                    changeRoute(target.group, event.target.value)
                  }
                >
                  <NativeSelectOption value='' disabled>
                    {t('Select model')}
                  </NativeSelectOption>
                  {models.map((model) => (
                    <NativeSelectOption key={model} value={model}>
                      {model}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              ) : (
                <Select
                  value={field.value}
                  disabled={props.disabled || models.length === 0}
                  onValueChange={(value) => {
                    if (value !== null) changeRoute(target.group, value)
                  }}
                >
                  <SelectTrigger
                    id={`monitor-model-${props.index}`}
                    onBlur={field.onBlur}
                    aria-invalid={Boolean(errors?.model)}
                    className='w-full'
                  >
                    <SelectValue placeholder={t('Select model')}>
                      {target.model || t('Select model')}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {models.map((model) => (
                        <SelectItem key={model} value={model}>
                          {model}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              )
            }
          />
          <FieldError>{errors?.model?.message}</FieldError>
        </Field>
      </TableCell>
      <TableCell className='align-top'>
        <Field
          data-invalid={Boolean(errors?.probe_token_id)}
          data-disabled={props.disabled}
        >
          <FieldLabel
            className='sr-only'
            htmlFor={`monitor-key-${props.index}`}
          >
            {t('Probe key')} {props.index + 1}
          </FieldLabel>
          <Controller
            control={form.control}
            name={`targets.${props.index}.probe_token_id`}
            render={({ field }) =>
              isMobile ? (
                <NativeSelect
                  id={`monitor-key-${props.index}`}
                  value={selectedToken ? String(selectedToken.id) : ''}
                  disabled={props.disabled || tokens.length === 0}
                  onBlur={field.onBlur}
                  aria-invalid={Boolean(errors?.probe_token_id)}
                  className='w-full'
                  onChange={(event) =>
                    field.onChange(Number(event.target.value))
                  }
                >
                  <NativeSelectOption value='' disabled>
                    {t('Select probe key')}
                  </NativeSelectOption>
                  {tokens.map((token) => (
                    <NativeSelectOption key={token.id} value={String(token.id)}>
                      {token.name?.trim() || `#${token.id}`}
                      {token.user_id ? ` (#${token.user_id})` : null}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              ) : (
                <Select
                  value={field.value > 0 ? String(field.value) : ''}
                  disabled={props.disabled || tokens.length === 0}
                  onValueChange={(value) => {
                    if (value !== null) field.onChange(Number(value))
                  }}
                >
                  <SelectTrigger
                    id={`monitor-key-${props.index}`}
                    onBlur={field.onBlur}
                    aria-invalid={Boolean(errors?.probe_token_id)}
                    className='w-full'
                  >
                    <SelectValue placeholder={t('Select probe key')}>
                      {selectedToken?.name?.trim() ||
                        (selectedToken
                          ? `#${selectedToken.id}`
                          : t('Select probe key'))}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {tokens.map((token) => (
                        <SelectItem key={token.id} value={String(token.id)}>
                          {token.name?.trim() || `#${token.id}`}{' '}
                          {token.user_id ? `(#${token.user_id})` : ''}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              )
            }
          />
          <FieldError>{errors?.probe_token_id?.message}</FieldError>
          {tokens.length === 0 && (
            <p className='text-destructive text-xs'>
              {t('No eligible key for this group/model. Create one in')}{' '}
              <Link className='underline' to='/keys'>
                {t('API Keys')}
              </Link>
              .
            </p>
          )}
        </Field>
      </TableCell>
      <TableCell className='align-top'>
        <Field
          data-invalid={Boolean(errors?.interval_seconds)}
          data-disabled={props.disabled}
        >
          <FieldLabel
            className='sr-only'
            htmlFor={`monitor-interval-${props.index}`}
          >
            {t('Maximum idle interval (seconds)')} {props.index + 1}
          </FieldLabel>
          <Input
            id={`monitor-interval-${props.index}`}
            type='number'
            min={60}
            max={3600}
            step={1}
            disabled={props.disabled}
            aria-invalid={Boolean(errors?.interval_seconds)}
            {...form.register(`targets.${props.index}.interval_seconds`, {
              setValueAs: (value: string) => (value === '' ? 0 : Number(value)),
            })}
          />
          <FieldError>{errors?.interval_seconds?.message}</FieldError>
        </Field>
      </TableCell>
      <TableCell className='align-top'>
        <Field
          data-invalid={Boolean(errors?.timeout_seconds)}
          data-disabled={props.disabled}
        >
          <FieldLabel
            className='sr-only'
            htmlFor={`monitor-timeout-${props.index}`}
          >
            {t('Timeout (seconds)')} {props.index + 1}
          </FieldLabel>
          <Input
            id={`monitor-timeout-${props.index}`}
            type='number'
            min={5}
            max={120}
            step={1}
            disabled={props.disabled}
            aria-invalid={Boolean(errors?.timeout_seconds)}
            {...form.register(`targets.${props.index}.timeout_seconds`, {
              setValueAs: (value: string) => (value === '' ? 0 : Number(value)),
            })}
          />
          <FieldError>{errors?.timeout_seconds?.message}</FieldError>
        </Field>
      </TableCell>
      <TableCell className='text-right align-top'>
        <Button
          type='button'
          variant='ghost'
          size='icon'
          aria-label={`${t('Remove route')} ${props.index + 1}`}
          title={t('Remove route')}
          onClick={props.onRemove}
          disabled={props.disabled}
        >
          <Trash2 className='text-destructive' aria-hidden='true' />
        </Button>
      </TableCell>
    </TableRow>
  )
}
