# Control Center UI

Feature code imports shared UI only from `@/ui`; subpath and relative bypasses
are unsupported. Tokens are semantic: add shared scale values to `tokens.css`,
add color/shadow values to both theme selectors with identical key sets, then
use `var(--cc-...)` from component CSS. Variants are closed, semantic unions.
DOM-bearing components forward refs, controls preserve native accessible props,
and interactive focus uses the shared focus border/shadow tokens.

<!-- @ui-export Button -->
<!-- @ui-export IconButton -->
<!-- @ui-export Input -->
<!-- @ui-export Select -->
<!-- @ui-export Textarea -->
<!-- @ui-export Text -->
<!-- @ui-export Stack -->
<!-- @ui-export Grid -->
<!-- @ui-export Badge -->
<!-- @ui-export StatusSignal -->
<!-- @ui-export Progress -->
<!-- @ui-export Tabs -->
<!-- @ui-export Tooltip -->
<!-- @ui-export Dialog -->
<!-- @ui-export Panel -->
<!-- @ui-export EmptyState -->
<!-- @ui-export Skeleton -->
<!-- @ui-export Spinner -->
<!-- @ui-export ActionBar -->
<!-- @ui-export DetailHeader -->
<!-- @ui-export ToolFrame -->
<!-- @ui-export ActivityIcon -->
<!-- @ui-export AlertIcon -->
<!-- @ui-export CheckIcon -->
<!-- @ui-export ChevronDownIcon -->
<!-- @ui-export ChevronLeftIcon -->
<!-- @ui-export ChevronRightIcon -->
<!-- @ui-export CloseIcon -->
<!-- @ui-export ContainerIcon -->
<!-- @ui-export DiffIcon -->
<!-- @ui-export ExternalLinkIcon -->
<!-- @ui-export InfoIcon -->
<!-- @ui-export LandmarkIcon -->
<!-- @ui-export ListChecksIcon -->
<!-- @ui-export MailIcon -->
<!-- @ui-export MessageIcon -->
<!-- @ui-export MoreIcon -->
<!-- @ui-export PauseIcon -->
<!-- @ui-export PlayIcon -->
<!-- @ui-export RefreshIcon -->
<!-- @ui-export SearchIcon -->
<!-- @ui-export StopIcon -->
<!-- @ui-export TerminalIcon -->
<!-- @ui-export TruckIcon -->
<!-- @ui-export ButtonProps -->
<!-- @ui-export IconButtonProps -->
<!-- @ui-export InputProps -->
<!-- @ui-export SelectProps -->
<!-- @ui-export TextareaProps -->
<!-- @ui-export TextProps -->
<!-- @ui-export StackProps -->
<!-- @ui-export GridProps -->
<!-- @ui-export BadgeProps -->
<!-- @ui-export StatusSignalProps -->
<!-- @ui-export ProgressProps -->
<!-- @ui-export TabsProps -->
<!-- @ui-export TooltipProps -->
<!-- @ui-export DialogProps -->
<!-- @ui-export PanelProps -->
<!-- @ui-export EmptyStateProps -->
<!-- @ui-export SkeletonProps -->
<!-- @ui-export SpinnerProps -->
<!-- @ui-export ActionBarProps -->
<!-- @ui-export DetailHeaderProps -->
<!-- @ui-export ToolFrameProps -->
<!-- @ui-export UIIcon -->
<!-- @ui-export ButtonVariant -->
<!-- @ui-export ButtonSize -->
<!-- @ui-export TextVariant -->
<!-- @ui-export Space -->
<!-- @ui-export StatusTone -->
<!-- @ui-export TabItem -->

```tsx
import {
  ActionBar, ActivityIcon, AlertIcon, Badge, Button, CheckIcon,
  ChevronDownIcon, ChevronLeftIcon, ChevronRightIcon, CloseIcon, ContainerIcon,
  DetailHeader, Dialog, DiffIcon, EmptyState, ExternalLinkIcon, Grid, IconButton,
  InfoIcon, Input, LandmarkIcon, ListChecksIcon, MailIcon, MessageIcon, MoreIcon,
  Panel, PauseIcon, PlayIcon, Progress, RefreshIcon, SearchIcon, Select, Skeleton,
  Spinner, Stack, StatusSignal, StopIcon, Tabs, TerminalIcon, Text, Textarea,
  ToolFrame, Tooltip, TruckIcon,
} from "@/ui";
import type {
  ActionBarProps, BadgeProps, ButtonProps, ButtonSize, ButtonVariant, DialogProps,
  DetailHeaderProps, EmptyStateProps, GridProps, IconButtonProps, InputProps,
  PanelProps, ProgressProps, SelectProps, SkeletonProps, Space, SpinnerProps,
  StackProps, StatusSignalProps, StatusTone, TabItem, TabsProps, TextareaProps,
  TextProps, TextVariant, ToolFrameProps, TooltipProps, UIIcon,
} from "@/ui";

const tabs: TabItem[] = [{ id: "work", label: "Work", content: <Text>Ready</Text> }];
const icon: UIIcon = ActivityIcon;
const variants: [ButtonVariant, ButtonSize, TextVariant, Space, StatusTone] =
  ["primary", "compact", "body", "3", "success"];
const props: Array<
  ActionBarProps | BadgeProps | ButtonProps | DialogProps | DetailHeaderProps |
  EmptyStateProps | GridProps | IconButtonProps | InputProps | PanelProps |
  ProgressProps | SelectProps | SkeletonProps | SpinnerProps | StackProps |
  StatusSignalProps | TabsProps | TextareaProps | TextProps | ToolFrameProps |
  TooltipProps
> = [];
void icon;
void variants;
void props;

export function Example() {
  return <ToolFrame header={<DetailHeader title="Convoy" />} sidebar={
    <Stack gap="2"><Input aria-label="Search" /><Select aria-label="State">
      <option>All</option></Select><Textarea aria-label="Comment" /></Stack>
  }><ActionBar aria-label="Actions"><Button>Run</Button><IconButton icon={TerminalIcon} aria-label="Open terminal" /></ActionBar>
    <Panel title="Health"><Grid gap="3"><StatusSignal label="Running" tone="success" />
      <Badge>3/4</Badge><Progress label="Progress" value={75} /><Tabs items={tabs} />
      <Tooltip content="Refresh"><Button>Refresh</Button></Tooltip><Dialog title="Details" trigger={<Button>Open</Button>}>Body</Dialog>
      <EmptyState title="No orders" /><Skeleton /><Spinner label="Loading" /></Grid></Panel>
    {[AlertIcon, CheckIcon, ChevronDownIcon, ChevronLeftIcon, ChevronRightIcon,
      CloseIcon, ContainerIcon, DiffIcon, ExternalLinkIcon, InfoIcon, LandmarkIcon,
      ListChecksIcon, MailIcon, MessageIcon, MoreIcon, PauseIcon, PlayIcon,
      RefreshIcon, SearchIcon, StopIcon, TruckIcon].map((Icon, index) => <Icon key={index} />)}
  </ToolFrame>;
}
```
