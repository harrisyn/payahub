// The standard form kit. Every form on the site builds from these, so fields
// look and behave the same everywhere and post plain FormData to server
// actions (multi-value fields as one entry per value; money in minor units;
// dates as ISO strings). Wrap a page or layout that uses them in <UIProvider>.
export { UIProvider } from "./provider";
export {
  Checkbox,
  MoneyInput,
  NumberInput,
  PasswordInput,
  SegmentedControl,
  Switch,
  Textarea,
  TextInput,
  type FrameProps,
} from "./inputs";
export {
  MultiSelect,
  RemoteMultiSelect,
  RemoteSelect,
  Select,
  TagsInput,
  type LookupSource,
  type Option,
} from "./select";
export { DateInput, DateRangeInput, DateTimeInput } from "./dates";
export { RichTextArea } from "./rich-text";
