<script setup lang="ts">
import { computed, reactive, ref, watch, onBeforeUnmount, onMounted } from "vue";

type PdfPage = {
  id: string;
  pageNumber: number;
  imageUrl: string;
  textUrl?: string;
  hasText: boolean;
  sourceText: string;
  translation: string;
  status: string;
  error?: string;
  updatedAt?: string;
};

type PdfTask = {
  id: string;
  fileName: string;
  totalPages: number;
  createdAt: string;
  updatedAt: string;
  combinedTxtUrl?: string;
  combinedPdfUrl?: string;
  formattedTxtUrl?: string;
  formattedByAI?: boolean;
  formattingOptimized?: boolean;
  formattingInProgress?: boolean;
  formattingTotalChunks?: number;
  formattingCompletedChunks?: number;
  pages: PdfPage[];
};

type ExportResponse = {
  task: PdfTask;
  url?: string;
};

type TaskSummary = {
  id: string;
  fileName: string;
  totalPages: number;
  completedPages: number;
  pendingPages: number;
  errorPages: number;
  createdAt: string;
  updatedAt: string;
};

type ProviderType = "openai" | "gemini" | "anthropic" | "custom";

type ProviderModelEntry = {
  id: string;
  name: string;
  apiType: ProviderType;
  enabled?: boolean;
};

type ProviderEntry = {
  id: string;
  name: string;
  type: ProviderType;
  baseUrl: string;
  apiKey: string;
  enabled?: boolean;
  models: ProviderModelEntry[];
};

type StoredConfig = {
  backendBase: string;
  providerList: ProviderEntry[];
  activeProviderId: string;
  activeModelMap: Record<string, string>;
};

const STORAGE_KEY = "pdftool_frontend_config";
const TASK_STORAGE_KEY = "pdftool_active_task_id";
const DEFAULT_MAX_TOKENS = 65535;

const providerTypeOptions = [
  { value: "openai" as ProviderType, label: "OpenAI", defaultBase: "https://api.openai.com/v1", defaultModel: "gpt-4o-mini" },
  { value: "gemini" as ProviderType, label: "Gemini", defaultBase: "https://generativelanguage.googleapis.com/v1", defaultModel: "gemini-1.5-flash" },
  { value: "anthropic" as ProviderType, label: "Anthropic", defaultBase: "https://api.anthropic.com/v1", defaultModel: "claude-3-5-sonnet" },
  { value: "custom" as ProviderType, label: "自定义", defaultBase: "", defaultModel: "" }
];
const modelApiTypeOptions = providerTypeOptions.filter((option) => option.value !== "custom");

const savedConfig = (() => {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? (JSON.parse(raw) as StoredConfig) : null;
  } catch {
    return null;
  }
})();

const savedTaskId = (() => {
  try {
    return localStorage.getItem(TASK_STORAGE_KEY) || "";
  } catch {
    return "";
  }
})();

const providerList = ref<ProviderEntry[]>(
  savedConfig?.providerList?.length
    ? savedConfig.providerList.map((provider) => normalizeProviderEntry(provider))
    : [createDefaultProvider("openai")]
);
if (!providerList.value.length) {
  providerList.value.push(createDefaultProvider("openai"));
} else {
  providerList.value = providerList.value.map((provider) => normalizeProviderEntry(provider));
}

const activeProviderId = ref<string>(
  savedConfig?.activeProviderId && providerList.value.some((p) => p.id === savedConfig.activeProviderId)
    ? savedConfig.activeProviderId
    : providerList.value[0].id
);

const activeModelMap = reactive<Record<string, string>>(savedConfig?.activeModelMap || {});
if (!activeModelMap[activeProviderId.value]) {
  activeModelMap[activeProviderId.value] = getFirstModelId(providerList.value[0]) || "";
}

const config = reactive({
  backendBase: savedConfig?.backendBase || "http://localhost:8090/api/pdf",
  providerBase: "",
  providerKey: "",
  providerModel: ""
});

const taskIdInput = ref(savedTaskId);
const lastTaskId = ref(savedTaskId);
const toast = reactive({ visible: false, text: "", type: "success" as "success" | "error" });
const task = ref<PdfTask | null>(null);
const uploading = ref(false);
const isExporting = reactive({ txtOriginal: false, txtFormatted: false, pdf: false });
const retranslateLoading = reactive<Record<number, boolean>>({});
const fileInput = ref<HTMLInputElement | null>(null);
const dragOverUpload = ref(false);
const selectedFileName = ref("");
const layoutLoading = ref(false);
const layoutStatus = ref<"idle" | "running" | "success" | "error">("idle");
const layoutStatusMessage = ref("");
const layoutNoticeVisible = ref(false);
let lastFormattingActive = false;
const showTxtMenu = ref(false);
const txtDropdownRef = ref<HTMLElement | null>(null);
const batchStatus = reactive({
	running: false,
	processed: 0,
	total: 0,
	message: "",
	paused: false
});
const selectedPages = ref<number[]>([]);
const translationRangeOptions = ["10", "20", "30", "50", "100", "all", "custom", "range"];
const translationBatchOptions = ["10", "20", "30", "50", "100", "all", "custom"];
const translationBatchMode = ref("10");
const translationCustomSize = ref("1");
const translationRangeMode = ref("all");
const translationRangeCustom = ref("1");
const translationRangeStart = ref("1");
const translationRangeEnd = ref("1");
const showSettings = ref(false);
const maxDisplaySize = 50;
const stalePendingMs = 2 * 60 * 1000;
const providerDialogVisible = ref(false);
const modelDialogVisible = ref(false);
const providerForm = reactive<{ id: string; name: string; type: ProviderType; baseUrl: string; apiKey: string; enabled: boolean }>({
  id: "",
  name: "",
  type: "openai",
  baseUrl: "",
  apiKey: "",
  enabled: true
});
const modelForm = reactive<{ id: string; name: string; apiType: ProviderType; providerId: string; enabled: boolean; maxTokens: number }>({
  id: "",
  name: "",
  apiType: "openai",
  providerId: "",
  enabled: true,
  maxTokens: DEFAULT_MAX_TOKENS
});
const modelDialogState = reactive<{
  providerId: string;
  available: ProviderModelEntry[];
  search: string;
  loading: boolean;
  error: string;
}>({
  providerId: "",
  available: [],
  search: "",
  loading: false,
  error: ""
});
const editingProviderId = ref<string | null>(null);
const editingModelId = ref<string | null>(null);
const fetchedModelsCache = reactive<Record<string, ProviderModelEntry[]>>({});
const modelTestStatus = reactive<Record<string, { status: "idle" | "testing" | "success" | "error"; message?: string }>>({});
const taskManagerVisible = ref(false);
const taskList = ref<TaskSummary[]>([]);
const taskListLoading = ref(false);
const taskListError = ref("");
const deletingTasks = reactive<Record<string, boolean>>({});
const currentPageIndex = ref(1);
const currentPageRange = computed(() => {
  const size = pageSize.value;
  const start = (currentPageIndex.value - 1) * size + 1;
  if (!task.value) return { start: 0, end: 0 };
  const end = Math.min(start + size - 1, task.value.pages.length);
  return { start, end };
});
let pollTimer: number | null = null;
const pollingDelay = 2500;

const activeProvider = computed(
  () => providerList.value.find((provider) => provider.id === activeProviderId.value) || providerList.value[0]
);
const layoutButtonLabel = computed(() => {
  if (layoutStatus.value === "running") return "AI 排版校对中...";
  if (layoutStatus.value === "success") return "重新 AI 排版校对";
  if (layoutStatus.value === "error") return "重试 AI 排版校对";
  return "AI 排版校对";
});
const layoutButtonClass = computed(() => {
  if (layoutStatus.value === "success") return "success";
  if (layoutStatus.value === "error") return "danger";
  return "";
});
const layoutProgressPercent = computed(() => {
  const current = task.value;
  if (!current || !current.formattingInProgress) {
    return 0;
  }
  const total = Math.max(0, Number(current.formattingTotalChunks) || 0);
  if (!total) {
    return 0;
  }
  const completed = Math.max(0, Math.min(total, Number(current.formattingCompletedChunks) || 0));
  return Math.min(100, Math.round((completed / total) * 100));
});
const layoutProgressText = computed(() => {
  if (!task.value || !task.value.formattingInProgress) {
    return "准备中...";
  }
  const total = Number(task.value.formattingTotalChunks) || 0;
  const completed = Number(task.value.formattingCompletedChunks) || 0;
  if (!total) {
    return "准备中...";
  }
  return `${completed} / ${total} (${layoutProgressPercent.value}%)`;
});
const activeProviderModels = computed(() => getEnabledModels(activeProvider.value));
const activeModelId = computed(() => {
  const providerId = activeProvider.value?.id;
  if (!providerId) return "";
  return activeModelMap[providerId] || getFirstModelId(activeProvider.value) || "";
});
const activeModel = computed(() => {
  if (!activeProvider.value) return null;
  return (
    activeProviderModels.value.find((model) => model.id === activeModelId.value) ||
    activeProvider.value.models.find((model) => model.id === activeModelId.value) ||
    activeProviderModels.value[0] ||
    activeProvider.value.models[0] ||
    null
  );
});
const activeModelMaxTokens = computed(() => {
  return activeModel.value?.maxTokens ?? DEFAULT_MAX_TOKENS;
});
const selectedModelId = computed({
  get: () => activeModelId.value,
  set: (val: string) => {
    if (!activeProvider.value) return;
    if (val && val !== activeModelId.value) {
      setActiveModel(activeProvider.value.id, val);
    }
  }
});
const filteredDialogModels = computed(() => {
  const list = modelDialogState.available || [];
  const keywords = modelDialogState.search
    .split(/\s+/)
    .map((item) => item.trim().toLowerCase())
    .filter(Boolean);
  if (!keywords.length) {
    return list;
  }
  return list.filter((model) => {
    const haystack = `${model.name || ""} ${model.id}`.toLowerCase();
    return keywords.every((keyword) => haystack.includes(keyword));
  });
});

function getModelTestKey(providerId: string, modelId: string) {
  return `${providerId}::${modelId}`;
}

function getModelStatus(providerId: string, modelId: string) {
  return modelTestStatus[getModelTestKey(providerId, modelId)];
}

watch(
  [activeProvider, activeModel],
  ([provider, model]) => {
    if (provider) {
      config.providerBase = provider.baseUrl;
      config.providerKey = provider.apiKey;
      config.providerModel = model?.id || "";
    }
  },
  { immediate: true }
);

watch(
  activeProviderId,
  (val) => {
    const provider = providerList.value.find((item) => item.id === val);
    if (!provider) return;
    const current = activeModelMap[val];
    const exists = provider.models.some((model) => model.id === current && model.enabled !== false);
    const fallbackId = getFirstModelId(provider);
    if ((!exists || !current) && fallbackId) {
      activeModelMap[val] = fallbackId;
    }
  },
  { immediate: true }
);

watch(
  providerList,
  (list) => {
    if (!list.length) {
      const fallback = createDefaultProvider("openai");
      providerList.value = [fallback];
      activeProviderId.value = fallback.id;
      activeModelMap[fallback.id] = getFirstModelId(fallback) || "";
    } else if (!list.some((p) => p.id === activeProviderId.value)) {
      activeProviderId.value = list[0].id;
      if (!activeModelMap[activeProviderId.value]) {
        activeModelMap[activeProviderId.value] = getFirstModelId(list[0]) || "";
      }
    }
  },
  { deep: true }
);


watch(
  () => ({
    backendBase: config.backendBase,
    providerList: providerList.value,
    activeProviderId: activeProviderId.value,
    activeModelMap: { ...activeModelMap }
  }),
  (value) => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(value));
    } catch {
      /* ignore */
    }
  },
  { deep: true }
);

const providerReady = computed(() => Boolean(config.providerKey?.trim()) && Boolean(config.providerModel?.trim()));
const backendReady = computed(() => Boolean(config.backendBase?.trim()));
const canUpload = computed(() => backendReady.value && providerReady.value && !uploading.value);

const pageSize = computed(() => {
  const limit = getTranslationLimit();
  if (!Number.isFinite(limit) || limit <= 0) {
    return maxDisplaySize;
  }
  return Math.max(1, Math.min(Math.floor(limit), maxDisplaySize));
});

watch(pageSize, () => {
  currentPageIndex.value = 1;
});

const totalPageCount = computed(() => {
  const total = task.value?.pages.length ?? 0;
  const size = pageSize.value || 1;
  return Math.max(1, Math.ceil(total / size));
});

watch(totalPageCount, (total) => {
  if (currentPageIndex.value > total) {
    currentPageIndex.value = total;
  }
});

const visiblePages = computed(() => {
  if (!task.value) return [];
  const size = pageSize.value;
  const start = (currentPageIndex.value - 1) * size;
  return task.value.pages.slice(start, start + size);
});

const failedPageNumbers = computed(() => {
  if (!task.value) return [];
  return task.value.pages.filter((page) => page.status === "error").map((p) => p.pageNumber);
});

const visibleFailedPageNumbers = computed(() => {
  return visiblePages.value.filter((page) => page.status === "error").map((p) => p.pageNumber);
});

function createDefaultProvider(type: ProviderType): ProviderEntry {
  const meta = getTypeMeta(type);
  return {
    id: generateId(),
    name: `${meta.label} 默认`,
    type,
    baseUrl: meta.defaultBase,
    apiKey: "",
    enabled: true,
    models: [
      {
        id: meta.defaultModel || `${meta.label.toLowerCase()}-model`,
        name: meta.defaultModel ? `${meta.label} 模型` : "自定义模型",
        apiType: type,
        enabled: true,
        maxTokens: DEFAULT_MAX_TOKENS
      }
    ]
  };
}

function generateId() {
  if (typeof crypto !== "undefined" && crypto.randomUUID) {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function getTypeMeta(type: ProviderType) {
  return providerTypeOptions.find((item) => item.value === type) || providerTypeOptions[0];
}

function getProviderLabel(type: ProviderType) {
  return getTypeMeta(type).label;
}

function getEnabledModels(provider?: ProviderEntry) {
  if (!provider) return [];
  const enabled = provider.models.filter((model) => model.enabled !== false);
  return enabled.length ? enabled : provider.models;
}

function getFirstModelId(provider?: ProviderEntry) {
  const models = getEnabledModels(provider);
  return models[0]?.id || provider?.models[0]?.id || "";
}

function getModelApiLabel(type: ProviderType) {
  if (type === "openai") return "OpenAI 兼容";
  if (type === "gemini") return "Gemini 兼容";
  if (type === "anthropic") return "Anthropic 兼容";
  return "OpenAI 兼容";
}

function sanitizeApiType(type: ProviderType | undefined, fallback: ProviderType): ProviderType {
  const allowed: ProviderType[] = ["openai", "gemini", "anthropic"];
  if (type && allowed.includes(type)) return type;
  if (allowed.includes(fallback)) return fallback;
  return "openai";
}

function normalizeModelEntry(model: ProviderModelEntry, fallbackType: ProviderType): ProviderModelEntry {
  const normalizedType = sanitizeApiType(model.apiType, fallbackType);
  const maxTokens = sanitizeMaxTokens(model.maxTokens);
  return {
    ...model,
    apiType: normalizedType,
    enabled: model.enabled !== false,
    maxTokens
  };
}

function normalizeProviderEntry(provider: ProviderEntry): ProviderEntry {
  const meta = getTypeMeta(provider.type);
  return {
    ...provider,
    enabled: provider.enabled !== false,
    baseUrl: provider.baseUrl || meta.defaultBase,
    models: (provider.models || []).map((model) => normalizeModelEntry(model, provider.type))
  };
}

function maskKey(key: string) {
  if (!key) return "";
  if (key.length <= 6) return "****";
  return `${key.slice(0, 3)}****${key.slice(-3)}`;
}

function commitProvider(providerId: string) {
  providerList.value = providerList.value.map((provider) =>
    provider.id === providerId ? { ...provider } : provider
  );
}

function normalizeBase(base: string) {
  if (!base) return "";
  return base.endsWith("/") ? base.slice(0, -1) : base;
}

function buildProviderModelsEndpoint(base: string, type: ProviderType) {
  const normalized = normalizeBase(base);
  if (type === "gemini") {
    if (normalized.includes("/models")) {
      return normalized;
    }
    if (normalized.includes("/v1beta")) {
      return `${normalized}/models`;
    }
    return `${normalized}/v1beta/models`;
  }
  return `${normalized}/models`;
}

function sanitizeModelId(id: string, type: ProviderType) {
  if (type === "gemini") {
    return id.replace(/^models\//i, "");
  }
  return id;
}

function sanitizeModelName(name: string, type: ProviderType) {
  if (type === "gemini") {
    return name.replace(/^models\//i, "");
  }
  return name;
}

function sanitizeMaxTokens(value?: number) {
  const num = Number(value);
  if (!Number.isFinite(num) || num <= 0) return DEFAULT_MAX_TOKENS;
  return Math.floor(num);
}

function buildApiUrl(path = "") {
  const base = normalizeBase(config.backendBase);
  return `${base}${path}`;
}

function resolveAssetUrl(path?: string) {
  if (!path) return "";
  if (/^https?:\/\//i.test(path)) return path;
  try {
    return new URL(path, config.backendBase).toString();
  } catch {
    return path;
  }
}

function showToast(text: string, type: "success" | "error" = "success") {
  toast.text = text;
  toast.type = type;
  toast.visible = true;
  setTimeout(() => {
    toast.visible = false;
  }, 2800);
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const url = buildApiUrl(path);
  const res = await fetch(url, options);
  if (!res.ok) {
    let message = "请求失败";
    try {
      const data = await res.json();
      message = (data as any).error || JSON.stringify(data);
    } catch {
      message = await res.text();
    }
    throw new Error(message || "请求失败");
  }
  return (await res.json()) as T;
}

function rememberTaskId(id?: string) {
  const next = (id || "").trim();
  lastTaskId.value = next;
  taskIdInput.value = next;
  try {
    if (next) {
      localStorage.setItem(TASK_STORAGE_KEY, next);
    } else {
      localStorage.removeItem(TASK_STORAGE_KEY);
    }
  } catch {
    /* ignore */
  }
}

function clearTaskState() {
  task.value = null;
  selectedPages.value = [];
  currentPageIndex.value = 1;
  stopPolling();
  rememberTaskId("");
  layoutLoading.value = false;
  layoutStatus.value = "idle";
  layoutStatusMessage.value = "";
  layoutNoticeVisible.value = false;
  lastFormattingActive = false;
}

async function loadTask() {
  const id = taskIdInput.value?.trim();
  if (!id) {
    showToast("请输入任务 ID", "error");
    return;
  }
  await loadTaskById(id);
}

async function loadTaskById(id: string, options: { silent?: boolean } = {}) {
  const trimmed = id?.trim();
  if (!trimmed) return;
  try {
    const data = await request<PdfTask>(`/tasks/${encodeURIComponent(trimmed)}`);
    setTaskData(data, true);
    if (!options.silent) {
      showToast("任务已恢复");
    }
  } catch (error: any) {
    if (!options.silent) {
      showToast(error.message || "加载任务失败", "error");
    }
    throw error;
  }
}

function triggerPick() {
  fileInput.value?.click();
}

async function onFileChange(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0];
  if (!file) return;
  selectedFileName.value = file.name;
  await uploadCurrentPdf(file);
  (event.target as HTMLInputElement).value = "";
}

async function handleDrop(event: DragEvent) {
  event.preventDefault();
  dragOverUpload.value = false;
  const file = event.dataTransfer?.files?.[0];
  if (!file) return;
  selectedFileName.value = file.name;
  await uploadCurrentPdf(file);
}

function handleDragOver(event: DragEvent) {
  event.preventDefault();
  dragOverUpload.value = true;
}

function handleDragLeave(event: DragEvent) {
  event.preventDefault();
  dragOverUpload.value = false;
}

async function uploadCurrentPdf(file: File) {
  if (!file.name.toLowerCase().endsWith(".pdf")) {
    showToast("仅支持 PDF 文件", "error");
    return;
  }
  if (!providerReady.value) {
    showToast("请先填写模型设置", "error");
    return;
  }
  uploading.value = true;
  try {
    const form = new FormData();
    form.append("file", file);
    form.append("provider_base", config.providerBase.trim());
    form.append("provider_key", config.providerKey.trim());
    form.append("provider_model", config.providerModel.trim());
    form.append("provider_type", activeProvider.value?.type || "openai");
    form.append("provider_api_type", activeModel.value?.apiType || activeProvider.value?.type || "openai");
    form.append("provider_max_tokens", String(activeModelMaxTokens.value));
    const batchLimitValue = translationBatchMode.value === "all" ? 0 : getTranslationLimit();
    form.append("initial_batch_limit", String(batchLimitValue));
    form.append("initial_range_mode", translationRangeMode.value);
    form.append("initial_range_custom", translationRangeCustom.value);
    form.append("initial_range_start", translationRangeStart.value);
    form.append("initial_range_end", translationRangeEnd.value);
    form.append("provider_api_type", activeModel.value?.apiType || activeProvider.value?.type || "openai");
    const data = await request<PdfTask>("/tasks", {
      method: "POST",
      body: form
    });
    setTaskData(data, true);
    showToast("上传并解析完成");
  } catch (error: any) {
    console.error(error);
    showToast(error.message || "上传失败", "error");
  } finally {
    uploading.value = false;
  }
}

function setTaskData(data: PdfTask, overrideId = false) {
  const isNewTask = task.value?.id !== data.id || overrideId;
  task.value = data;
  const allowed = new Set(data.pages.map((page) => page.pageNumber));
  if (isNewTask) {
    rememberTaskId(data.id);
    currentPageIndex.value = 1;
    selectedPages.value = [];
  } else {
    selectedPages.value = selectedPages.value.filter((num) => allowed.has(num));
  }
  syncLayoutIndicators(task.value);
  ensurePolling();
}

function syncLayoutIndicators(current: PdfTask | null) {
  if (!current) {
    layoutLoading.value = false;
    if (layoutStatus.value === "running") {
      layoutStatus.value = "idle";
      layoutStatusMessage.value = "";
    }
    layoutNoticeVisible.value = false;
    lastFormattingActive = false;
    return;
  }
  const nowActive = Boolean(current.formattingInProgress);
  if (nowActive) {
    layoutStatus.value = "running";
    layoutLoading.value = true;
    layoutNoticeVisible.value = true;
    layoutStatusMessage.value = "";
    lastFormattingActive = true;
    return;
  }
  layoutLoading.value = false;
  if (lastFormattingActive) {
    if (current.formattedByAI) {
      layoutStatus.value = "success";
      layoutStatusMessage.value = "AI 排版完成";
      layoutNoticeVisible.value = true;
    } else if (layoutStatus.value === "running") {
      layoutStatus.value = "idle";
      layoutStatusMessage.value = "";
      layoutNoticeVisible.value = false;
    }
  } else if (current.formattedByAI && (layoutStatus.value === "idle" || layoutStatus.value === "running")) {
    layoutStatus.value = "success";
    if (!layoutStatusMessage.value) {
      layoutStatusMessage.value = "AI 排版完成";
    }
  }
  lastFormattingActive = false;
}

function dismissLayoutNotice() {
  if (task.value?.formattingInProgress) {
    return;
  }
  layoutNoticeVisible.value = false;
}

async function sendRetranslate(pageNumber: number) {
  if (!task.value) throw new Error("任务不存在");
  const body = {
    provider_base: config.providerBase.trim() || undefined,
    provider_key: config.providerKey.trim(),
    provider_model: config.providerModel.trim(),
    provider_type: activeProvider.value?.type || "openai",
    provider_api_type: activeModel.value?.apiType || activeProvider.value?.type || "openai",
    provider_max_tokens: activeModelMaxTokens.value
  };
  return request<PdfTask>(`/tasks/${task.value.id}/pages/${pageNumber}/retranslate`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body)
  });
}

function getTranslationLimit() {
  if (translationBatchMode.value === "all") return Number.POSITIVE_INFINITY;
  const val =
    translationBatchMode.value === "custom"
      ? Number(translationCustomSize.value)
      : Number(translationBatchMode.value);
  if (!Number.isFinite(val) || val <= 0) return 10;
  return Math.max(1, Math.floor(val));
}

function resolveRangePages() {
  if (!task.value) return [];
  const pages = task.value.pages.map((page) => page.pageNumber);
  if (!pages.length) return [];
  if (translationRangeMode.value === "all") {
    return pages;
  }
  if (translationRangeMode.value === "range") {
    let start = Number(translationRangeStart.value);
    let end = Number(translationRangeEnd.value);
    if (!Number.isFinite(start) || !Number.isFinite(end)) {
      return [];
    }
    if (start > end) [start, end] = [end, start];
    start = Math.max(1, Math.floor(start));
    end = Math.min(task.value.pages.length, Math.floor(end));
    const range: number[] = [];
    for (let num = start; num <= end; num++) {
      range.push(num);
    }
    return range;
  }
  const limit =
    translationRangeMode.value === "custom"
      ? Number(translationRangeCustom.value)
      : Number(translationRangeMode.value);
  if (!Number.isFinite(limit) || limit <= 0) {
    return [];
  }
  return pages.slice(0, Math.min(Math.floor(limit), pages.length));
}

function goToPage(delta: number) {
  const next = currentPageIndex.value + delta;
  if (next < 1 || next > totalPageCount.value) return;
  currentPageIndex.value = next;
}

function isSelected(pageNumber: number) {
  return selectedPages.value.includes(pageNumber);
}

function togglePageSelection(pageNumber: number) {
  if (isSelected(pageNumber)) {
    selectedPages.value = selectedPages.value.filter((num) => num !== pageNumber);
  } else {
    selectedPages.value = uniqueSortedPages([...selectedPages.value, pageNumber]);
  }
}

function selectVisiblePages() {
  const numbers = visiblePages.value.map((page) => page.pageNumber);
  selectedPages.value = uniqueSortedPages([...selectedPages.value, ...numbers]);
}

function selectAllPages() {
  if (!task.value) return;
  selectedPages.value = uniqueSortedPages(task.value.pages.map((page) => page.pageNumber));
}

function clearSelection() {
  selectedPages.value = [];
}

async function exportTxt(variant: "original" | "formatted") {
  if (!task.value) return;
  if (variant === "formatted" && !task.value.formattedByAI) {
    showToast("尚未生成 AI 排版版本", "error");
    return;
  }
  showTxtMenu.value = false;
  const key = variant === "formatted" ? "txtFormatted" : "txtOriginal";
  isExporting[key] = true;
  try {
    const suffix = variant === "formatted" ? "?variant=formatted" : "";
    const resp = await request<ExportResponse>(`/tasks/${task.value.id}/export/txt${suffix}`, { method: "POST" });
    setTaskData(resp.task);
    if (resp.url) {
      window.open(resolveAssetUrl(resp.url), "_blank", "noopener");
    }
    showToast(variant === "formatted" ? "已生成 AI 排版 TXT" : "已生成原版 TXT");
  } catch (error: any) {
    console.error(error);
    showToast(error.message || "导出失败", "error");
  } finally {
    isExporting[key] = false;
  }
}

async function exportPdf() {
  if (!task.value) return;
  isExporting.pdf = true;
  try {
    const resp = await request<ExportResponse>(`/tasks/${task.value.id}/export/pdf`, { method: "POST" });
    setTaskData(resp.task);
    if (resp.url) {
      window.open(resolveAssetUrl(resp.url), "_blank", "noopener");
    }
    showToast("已生成 PDF 文件");
  } catch (error: any) {
    console.error(error);
    showToast(error.message || "导出失败", "error");
  } finally {
    isExporting.pdf = false;
  }
}

async function runAiLayout() {
  if (!task.value) return;
  if (!providerReady.value) {
    showToast("请先填写模型设置", "error");
    return;
  }
  layoutLoading.value = true;
  layoutStatus.value = "running";
  layoutStatusMessage.value = "";
  layoutNoticeVisible.value = true;
  if (task.value) {
    task.value.formattingInProgress = true;
    task.value.formattingCompletedChunks = 0;
    task.value.formattingTotalChunks = task.value.formattingTotalChunks || 0;
    syncLayoutIndicators(task.value);
    ensurePolling();
  }
  try {
    const body = {
      provider_type: activeProvider.value?.type || "openai",
      provider_api_type: activeModel.value?.apiType || activeProvider.value?.type || "openai",
      provider_base: config.providerBase.trim() || undefined,
      provider_key: config.providerKey.trim(),
      provider_model: config.providerModel.trim(),
      provider_max_tokens: activeModelMaxTokens.value
    };
    const resp = await request<ExportResponse>(`/tasks/${task.value.id}/layout`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    });
    setTaskData(resp.task, true);
    layoutStatus.value = "success";
    layoutStatusMessage.value = "AI 排版完成";
    layoutNoticeVisible.value = true;
    showToast("AI 排版校对完成");
  } catch (error: any) {
    console.error(error);
    showToast(error.message || "AI 排版失败", "error");
    layoutStatus.value = "error";
    layoutStatusMessage.value = error.message || "AI 排版失败";
    if (task.value) {
      task.value.formattingInProgress = false;
      syncLayoutIndicators(task.value);
    }
    lastFormattingActive = false;
    layoutNoticeVisible.value = true;
  } finally {
    layoutLoading.value = false;
  }
}

function handleTxtMenuOutside(event: MouseEvent) {
  if (!showTxtMenu.value) return;
  const el = txtDropdownRef.value;
  if (el && !el.contains(event.target as Node)) {
    showTxtMenu.value = false;
  }
}

function uniqueSortedPages(list: number[]) {
  const next = Array.from(new Set(list));
  next.sort((a, b) => a - b);
  return next;
}

type RetryOptions = {
  honorBatchLimit?: boolean;
  processAllBatches?: boolean;
};

async function retryPages(pageNumbers: number[], options: RetryOptions = {}) {
  const { honorBatchLimit = true, processAllBatches = false } = options;
  if (!task.value) return;
  if (!providerReady.value) {
    showToast("请先填写模型设置", "error");
    return;
  }
  const validPages = uniqueSortedPages(pageNumbers).filter((page) => task.value?.pages.some((p) => p.pageNumber === page));
  if (!validPages.length) {
    showToast("没有可处理的页面", "error");
    return;
  }
  const computeBatchSize = (remaining: number) => {
    if (!honorBatchLimit) {
      return remaining;
    }
    const limitValue = getTranslationLimit();
    if (!Number.isFinite(limitValue) || limitValue <= 0) {
      return Math.max(1, remaining);
    }
    return Math.max(1, Math.min(Math.floor(limitValue), remaining));
  };
  const computeTotalTarget = () => {
    if (processAllBatches) {
      return validPages.length;
    }
    return computeBatchSize(validPages.length);
  };
  const totalTarget = computeTotalTarget();
  if (!totalTarget) {
    showToast("每批页数配置无效", "error");
    return;
  }

  batchStatus.running = true;
  batchStatus.processed = 0;
  batchStatus.total = totalTarget;
  batchStatus.message = "";
  batchStatus.paused = false;
  const waitIfPaused = async () => {
    while (batchStatus.paused) {
      await new Promise((resolve) => setTimeout(resolve, 300));
    }
  };

  const processBatch = async (batch: number[]) => {
    await waitIfPaused();
    const tasks = batch.map((pageNumber) => {
      retranslateLoading[pageNumber] = true;
      return sendRetranslate(pageNumber)
        .then((data) => {
          setTaskData(data);
          batchStatus.processed += 1;
          selectedPages.value = selectedPages.value.filter((num) => num !== pageNumber);
        })
        .catch((error: any) => {
          console.error(error);
          showToast(`第 ${pageNumber} 页失败：${error.message || "接口错误"}`, "error");
        })
        .finally(() => {
          retranslateLoading[pageNumber] = false;
        });
    });
    await Promise.all(tasks);
  };

  try {
    let remaining = [...validPages];
    while (remaining.length > 0) {
      await waitIfPaused();
      const batchSize = computeBatchSize(remaining.length);
      if (batchSize <= 0) {
        break;
      }
      const batch = remaining.slice(0, batchSize);
      await processBatch(batch);
      remaining = remaining.slice(batchSize);
      if (!processAllBatches) {
        break;
      }
    }
    batchStatus.message = `批量翻译完成：${batchStatus.processed}/${batchStatus.total}`;
  } finally {
    batchStatus.running = false;
    batchStatus.paused = false;
    setTimeout(() => {
      batchStatus.message = "";
    }, 4000);
  }
}

async function retrySelectedPages() {
  await retryPages(selectedPages.value);
}

async function retryVisibleBatch() {
  const visible = visiblePages.value.map((page) => page.pageNumber);
  await retryPages(visible);
}

async function retryRangePages() {
  const pages = resolveRangePages();
  if (!pages.length) {
    showToast("请设置有效的翻译范围", "error");
    return;
  }
  await retryPages(pages, { honorBatchLimit: false });
}

async function retryVisibleFailedPages() {
  if (!visibleFailedPageNumbers.value.length) {
    showToast("当前页没有失败的页面", "error");
    return;
  }
  await retryPages(visibleFailedPageNumbers.value, { honorBatchLimit: false });
}

async function retryAllFailedPages() {
  if (!failedPageNumbers.value.length) {
    showToast("没有失败页面", "error");
    return;
  }
  await retryPages(failedPageNumbers.value, { processAllBatches: true });
}

function toggleBatchPause() {
  if (!batchStatus.running) {
    return;
  }
  batchStatus.paused = !batchStatus.paused;
}

function taskSummaryStatus(summary: TaskSummary) {
  if (summary.pendingPages > 0) return "pending";
  if (summary.errorPages > 0) return "error";
  return "completed";
}

function taskSummaryLabel(summary: TaskSummary) {
  const status = taskSummaryStatus(summary);
  if (status === "pending") return "处理中";
  if (status === "error") {
    return summary.completedPages > 0 ? "部分失败" : "失败";
  }
  return "已完成";
}

function taskProgressText(summary: TaskSummary) {
  return `完成 ${summary.completedPages}/${summary.totalPages} · 失败 ${summary.errorPages} · 待处理 ${summary.pendingPages}`;
}

async function fetchTaskList(options: { silent?: boolean } = {}) {
  taskListLoading.value = true;
  taskListError.value = "";
  try {
    const data = await request<{ tasks: TaskSummary[] }>("/tasks");
    taskList.value = data.tasks || [];
  } catch (error: any) {
    taskListError.value = error.message || "获取任务列表失败";
    if (!options.silent) {
      showToast(taskListError.value, "error");
    }
  } finally {
    taskListLoading.value = false;
  }
}

function openTaskManager() {
  taskManagerVisible.value = true;
  fetchTaskList({ silent: true });
}

function closeTaskManager() {
  taskManagerVisible.value = false;
}

async function handleTaskSelect(summary: TaskSummary) {
  taskManagerVisible.value = false;
  taskIdInput.value = summary.id;
  try {
    await loadTaskById(summary.id);
  } catch {
    /* errors handled inside loadTaskById */
  }
}

async function deleteTaskEntry(id: string) {
  const trimmed = id?.trim();
  if (!trimmed) return;
  deletingTasks[trimmed] = true;
  try {
    await request(`/tasks/${encodeURIComponent(trimmed)}`, { method: "DELETE" });
    if (task.value?.id === trimmed) {
      clearTaskState();
    }
    showToast("任务已删除");
    await fetchTaskList({ silent: true });
  } catch (error: any) {
    showToast(error.message || "删除任务失败", "error");
  } finally {
    deletingTasks[trimmed] = false;
  }
}

function openSettings() {
  showSettings.value = true;
}

function closeSettings() {
  showSettings.value = false;
  providerDialogVisible.value = false;
  modelDialogVisible.value = false;
}

function handleSaveSettings() {
  closeSettings();
  showToast("设置已保存");
}

function openProviderDialog(provider?: ProviderEntry) {
  if (provider) {
    editingProviderId.value = provider.id;
    providerForm.id = provider.id;
    providerForm.name = provider.name;
    providerForm.type = provider.type;
    providerForm.baseUrl = provider.baseUrl;
    providerForm.apiKey = provider.apiKey;
    providerForm.enabled = provider.enabled !== false;
  } else {
    editingProviderId.value = null;
    providerForm.id = "";
    providerForm.name = "";
    providerForm.type = "openai";
    providerForm.baseUrl = getTypeMeta("openai").defaultBase;
    providerForm.apiKey = "";
    providerForm.enabled = true;
  }
  providerDialogVisible.value = true;
}

function handleProviderTypeChange() {
  const meta = getTypeMeta(providerForm.type);
  providerForm.baseUrl = meta.defaultBase;
}

function saveProvider() {
  const payload: ProviderEntry = {
    id: editingProviderId.value || generateId(),
    name: providerForm.name || `${getProviderLabel(providerForm.type)} 提供商`,
    type: providerForm.type,
    baseUrl: providerForm.baseUrl || getTypeMeta(providerForm.type).defaultBase,
    apiKey: providerForm.apiKey,
    enabled: providerForm.enabled !== false,
    models: []
  };
  const defaultModel = getTypeMeta(providerForm.type).defaultModel;
  payload.models = editingProviderId.value
    ? (providerList.value.find((p) => p.id === editingProviderId.value)?.models || []).map((model) =>
        normalizeModelEntry(model, providerForm.type)
      )
    : [
        {
          id: defaultModel || `${providerForm.type}-model`,
          name: defaultModel ? `${getProviderLabel(providerForm.type)} 模型` : "自定义模型",
          apiType: providerForm.type,
          enabled: true
        }
      ];

  const normalized = normalizeProviderEntry(payload);
  if (editingProviderId.value) {
    providerList.value = providerList.value.map((provider) => (provider.id === normalized.id ? normalized : provider));
  } else {
    providerList.value = [...providerList.value, normalized];
  }
  providerDialogVisible.value = false;
  if (!activeProviderId.value) {
    activeProviderId.value = payload.id;
  }
  if (!activeModelMap[payload.id]) {
    activeModelMap[payload.id] = getFirstModelId(providerList.value.find((p) => p.id === payload.id)) || "";
  }
}

function deleteProvider(providerId: string) {
  providerList.value = providerList.value.filter((provider) => provider.id !== providerId);
  delete activeModelMap[providerId];
  if (activeProviderId.value === providerId) {
    activeProviderId.value = providerList.value[0]?.id || "";
  }
}

function setActiveProvider(providerId: string) {
  activeProviderId.value = providerId;
  if (!activeModelMap[providerId]) {
    const provider = providerList.value.find((item) => item.id === providerId);
    activeModelMap[providerId] = getFirstModelId(provider) || "";
  }
}

function openModelDialog(providerId: string, model?: ProviderModelEntry) {
  modelForm.providerId = providerId;
  const providerType = providerList.value.find((p) => p.id === providerId)?.type || "openai";
  if (model) {
    editingModelId.value = model.id;
    modelForm.id = model.id;
    modelForm.name = model.name;
    modelForm.apiType = sanitizeApiType(model.apiType, providerType);
    modelForm.enabled = model.enabled !== false;
    modelForm.maxTokens = model.maxTokens ?? DEFAULT_MAX_TOKENS;
  } else {
    editingModelId.value = null;
    modelForm.id = "";
    modelForm.name = "";
    modelForm.apiType = sanitizeApiType(providerType, providerType);
    modelForm.enabled = true;
    modelForm.maxTokens = DEFAULT_MAX_TOKENS;
  }
  modelDialogState.providerId = providerId;
  modelDialogState.available = fetchedModelsCache[providerId] || [];
  modelDialogState.search = "";
  modelDialogState.error = "";
  modelDialogState.loading = false;
  modelDialogVisible.value = true;
}

function saveModel() {
  const provider = providerList.value.find((item) => item.id === modelForm.providerId);
  if (!provider) return;
  const payload: ProviderModelEntry = {
    id: modelForm.id || generateId(),
    name: modelForm.name || `${getProviderLabel(modelForm.apiType)} 模型`,
    apiType: modelForm.apiType,
    enabled: modelForm.enabled !== false,
    maxTokens: sanitizeMaxTokens(modelForm.maxTokens)
  };
  const normalized = normalizeModelEntry(payload, provider.type);
  if (editingModelId.value) {
    provider.models = provider.models.map((model) => (model.id === editingModelId.value ? normalized : model));
  } else {
    provider.models.push(normalized);
  }
  providerList.value = [...providerList.value];
  modelDialogVisible.value = false;
  if (!activeModelMap[provider.id]) {
    activeModelMap[provider.id] = getFirstModelId(provider) || payload.id;
  }
}

function deleteModel(providerId: string, modelId: string) {
  const provider = providerList.value.find((item) => item.id === providerId);
  if (!provider) return;
  provider.models = provider.models.filter((model) => model.id !== modelId);
  providerList.value = [...providerList.value];
  if (activeModelMap[providerId] === modelId) {
    activeModelMap[providerId] = getFirstModelId(provider) || "";
  }
}

function setActiveModel(providerId: string, modelId: string) {
  activeModelMap[providerId] = modelId;
  if (providerId === activeProviderId.value) {
    ensurePolling();
  }
}

async function fetchProviderModelList(provider: ProviderEntry, options: { quiet?: boolean } = {}): Promise<ProviderModelEntry[]> {
  const base = provider.baseUrl?.trim();
  if (!base) {
    const err = new Error("请先填写 API URL");
    if (!options.quiet) {
      showToast(err.message, "error");
    }
    throw err;
  }
  const endpoint = buildProviderModelsEndpoint(base, provider.type);
  const headers: Record<string, string> = {};
  if (provider.apiKey) {
    if (provider.type === "gemini") {
      headers["x-goog-api-key"] = provider.apiKey;
    } else {
      headers.Authorization = `Bearer ${provider.apiKey}`;
    }
  }
  try {
    const res = await fetch(endpoint, { method: "GET", headers });
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || "获取模型失败");
    }
    const data = await res.json();
    const rawList =
      (Array.isArray((data as any)?.data) && (data as any).data) ||
      (Array.isArray((data as any)?.models) && (data as any).models) ||
      (Array.isArray((data as any)?.result) && (data as any).result) ||
      (Array.isArray(data) && data) ||
      [];
    const normalized = (rawList as any[]).map((item, index) => {
      if (typeof item === "string") {
        const cleanId = sanitizeModelId(item, provider.type);
        return {
          id: cleanId,
          name: sanitizeModelName(cleanId, provider.type),
          apiType: provider.type,
          maxTokens: DEFAULT_MAX_TOKENS
        };
      }
      if (!item || typeof item !== "object") {
        return null;
      }
      const rawId = item.id || item.model || item.name || `model_${index}`;
      const id = sanitizeModelId(rawId, provider.type);
      if (!id) return null;
      const rawName = item.name || item.displayName || item.id || `模型 ${index + 1}`;
      const name = sanitizeModelName(rawName, provider.type);
      const apiType = ((item.apiType as ProviderType) || provider.type) as ProviderType;
      const maxTokens = sanitizeMaxTokens((item as any)?.maxTokens);
      return { id, name, apiType, maxTokens };
    });
    const filtered = normalized.filter((item): item is ProviderModelEntry => Boolean(item && item.id));
    fetchedModelsCache[provider.id] = filtered;
    if (modelDialogVisible.value && modelDialogState.providerId === provider.id) {
      modelDialogState.available = filtered;
    }
    if (!options.quiet) {
      if (!filtered.length) {
        showToast("未获取到可用模型", "error");
      } else {
        showToast(`获取到 ${filtered.length} 个模型`);
      }
    }
    return filtered;
  } catch (error: any) {
    console.error(error);
    if (!options.quiet) {
      showToast(error.message || "通过提供商 API 获取失败", "error");
    }
    throw error;
  }
}

async function fetchDialogModelList() {
  if (!modelForm.providerId) return;
  const provider = providerList.value.find((item) => item.id === modelForm.providerId);
  if (!provider) return;
  modelDialogState.loading = true;
  modelDialogState.error = "";
  try {
    const models = await fetchProviderModelList(provider, { quiet: true });
    modelDialogState.available = models;
    fetchedModelsCache[provider.id] = models;
    if (!models.length) {
      modelDialogState.error = "未获取到可用模型";
    }
  } catch (error: any) {
    modelDialogState.error = error.message || "获取模型列表失败";
  } finally {
    modelDialogState.loading = false;
  }
}

function selectDialogModel(model: ProviderModelEntry) {
  modelForm.id = model.id;
  modelForm.name = model.name || model.id;
  modelForm.apiType = sanitizeApiType(model.apiType, modelForm.apiType);
  modelDialogState.search = model.id;
}

function resolveModelApiType(model: ProviderModelEntry, provider: ProviderEntry): ProviderType {
  return sanitizeApiType(model.apiType, provider.type);
}

async function testModelConnection(providerId: string, modelId: string) {
  const provider = providerList.value.find((item) => item.id === providerId);
  if (!provider) {
    showToast("未找到提供商", "error");
    return;
  }
  const model = provider.models.find((item) => item.id === modelId);
  if (!model) {
    showToast("未找到模型", "error");
    return;
  }
  if (!provider.apiKey?.trim()) {
    showToast("请先填写 API Key", "error");
    return;
  }
  const key = getModelTestKey(providerId, modelId);
  modelTestStatus[key] = { status: "testing" };
  try {
    await runModelTestRequest(provider, model);
    modelTestStatus[key] = { status: "success", message: "可用" };
    showToast(`${model.name || model.id} 测试成功`);
  } catch (error: any) {
    console.error(error);
    modelTestStatus[key] = { status: "error", message: error.message || "测试失败" };
    showToast(error.message || "测试失败", "error");
  }
}

async function runModelTestRequest(provider: ProviderEntry, model: ProviderModelEntry) {
  const apiType = resolveModelApiType(model, provider);
  if (apiType === "gemini") {
    return testGeminiModel(provider, model);
  }
  if (apiType === "anthropic") {
    return testAnthropicModel(provider, model);
  }
  return testOpenAIModel(provider, model);
}

async function ensureResponseOk(res: Response) {
  if (res.ok) return;
  let message = "";
  try {
    const data = await res.json();
    message = (data as any)?.error?.message || (data as any)?.message || JSON.stringify(data);
  } catch {
    message = await res.text();
  }
  throw new Error(message || `请求失败（${res.status}）`);
}

function withBearer(headers: Record<string, string>, key: string) {
  headers.Authorization = `Bearer ${key}`;
  return headers;
}

async function testOpenAIModel(provider: ProviderEntry, model: ProviderModelEntry) {
  const base = normalizeBase(provider.baseUrl || getTypeMeta("openai").defaultBase);
  if (!base) {
    throw new Error("请设置 API URL");
  }
  const endpoint = /\/chat\/completions$/i.test(base) ? base : `${base}/chat/completions`;
  const headers: Record<string, string> = withBearer({ "Content-Type": "application/json" }, provider.apiKey.trim());
  const body = {
    model: model.id,
    max_tokens: 5,
    messages: [{ role: "user", content: "ping" }]
  };
  const res = await fetch(endpoint, { method: "POST", headers, body: JSON.stringify(body) });
  await ensureResponseOk(res);
}

async function testGeminiModel(provider: ProviderEntry, model: ProviderModelEntry) {
  const base = normalizeBase(provider.baseUrl || getTypeMeta("gemini").defaultBase);
  if (!base) {
    throw new Error("请设置 Gemini API URL");
  }
  const prefix = base.includes("/models/") ? base : `${base}/models/${encodeURIComponent(model.id)}`;
  const endpoint = prefix.endsWith(":generateContent") ? prefix : `${prefix}:generateContent`;
  const url = `${endpoint}?key=${encodeURIComponent(provider.apiKey.trim())}`;
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  const body = {
    contents: [
      {
        role: "user",
        parts: [{ text: "ping" }]
      }
    ],
    generationConfig: { maxOutputTokens: 32 }
  };
  const res = await fetch(url, { method: "POST", headers, body: JSON.stringify(body) });
  await ensureResponseOk(res);
}

async function testAnthropicModel(provider: ProviderEntry, model: ProviderModelEntry) {
  const base = normalizeBase(provider.baseUrl || getTypeMeta("anthropic").defaultBase);
  if (!base) {
    throw new Error("请设置 Anthropic API URL");
  }
  const endpoint = base.endsWith("/messages") ? base : `${base}/messages`;
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "x-api-key": provider.apiKey.trim(),
    "anthropic-version": "2023-06-01"
  };
  const body = {
    model: model.id,
    max_tokens: 64,
    messages: [{ role: "user", content: "ping" }]
  };
  const res = await fetch(endpoint, { method: "POST", headers, body: JSON.stringify(body) });
  await ensureResponseOk(res);
}

function copyText(text?: string) {
  if (!text) return;
  navigator.clipboard.writeText(text).then(
    () => showToast("已复制译文"),
    () => showToast("复制失败", "error")
  );
}

function handleManualTranslation(page: PdfPage) {
  if (!page.translation) return;
  page.status = "completed";
  page.hasText = true;
  page.error = "";
  page.updatedAt = new Date().toISOString();
}

function statusLabel(status?: string) {
  if (status === "completed") return "完成";
  if (status === "error") return "失败";
  return "进行中";
}

function isPendingAndFresh(page: PdfPage) {
  if (page.status !== "pending") return false;
  if (!page.updatedAt) return true;
  const ts = new Date(page.updatedAt).getTime();
  if (!Number.isFinite(ts)) return true;
  return Date.now() - ts < stalePendingMs;
}

function formatDate(value?: string) {
  if (!value) return "";
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString();
}

onBeforeUnmount(() => {
  stopPolling();
  window.removeEventListener("click", handleTxtMenuOutside);
});

onMounted(() => {
  window.addEventListener("click", handleTxtMenuOutside);
  if (lastTaskId.value) {
    loadTaskById(lastTaskId.value, { silent: true }).catch(() => rememberTaskId(""));
  }
});

function needsPollingTask(data: PdfTask) {
  if (data.formattingInProgress) {
    return true;
  }
  return data.pages.some((page) => isPendingAndFresh(page));
}

function stopPolling() {
  if (pollTimer !== null) {
    window.clearTimeout(pollTimer);
    pollTimer = null;
  }
}

function ensurePolling() {
  if (!task.value || !needsPollingTask(task.value)) {
    stopPolling();
    return;
  }
  if (pollTimer === null) {
    pollTimer = window.setTimeout(pollTask, pollingDelay);
  }
}

async function pollTask() {
  if (!task.value) {
    stopPolling();
    return;
  }
  try {
    const data = await request<PdfTask>(`/tasks/${task.value.id}`);
    task.value = data;
    syncLayoutIndicators(task.value);
  } catch (error) {
    console.error("轮询失败", error);
    stopPolling();
    return;
  }
  if (task.value && needsPollingTask(task.value)) {
    stopPolling();
    pollTimer = window.setTimeout(pollTask, pollingDelay);
  } else {
    stopPolling();
  }
}
</script>

<template>
  <div class="app">
    <header class="hero">
      <div class="hero-left">
        <h1>PDF 图像翻译工具</h1>
        <p class="muted">多模型提供商 · 分批翻译 · 支持恢复历史任务</p>
      </div>
      <div class="hero-right">
        <div class="model-picker">
          <label>AI 模型：</label>
          <select v-model="activeProviderId">
            <option v-for="provider in providerList" :key="provider.id" :value="provider.id">
              {{ provider.name }}
            </option>
          </select>
          <select v-model="selectedModelId">
            <option v-for="model in activeProviderModels" :key="model.id" :value="model.id">
              {{ model.name || model.id }}
            </option>
          </select>
        </div>
        <div class="task-controls">
          <input v-model="taskIdInput" type="text" placeholder="输入任务 ID 快速恢复" />
          <button class="ghost" type="button" @click="loadTask" :disabled="!taskIdInput || !taskIdInput.trim()">载入任务</button>
          <button class="ghost" type="button" @click="openTaskManager">任务管理</button>
          <button class="ghost" type="button" @click="openSettings">设置</button>
        </div>
      </div>
    </header>

    <section class="card">
      <h2>上传 PDF</h2>
      <p class="muted">拖拽或点击上传 PDF 文件，系统会自动拆页并翻译。</p>
      <div
        class="upload-bar dropzone"
        :class="{ 'dropzone--active': dragOverUpload }"
        @dragover="handleDragOver"
        @dragenter="handleDragOver"
        @dragleave="handleDragLeave"
        @drop="handleDrop"
        @click="triggerPick"
      >
        <div class="upload-info">
          <strong>{{ selectedFileName || "拖拽或点击选择 PDF" }}</strong>
          <span>{{ uploading ? "上传中..." : "仅支持 PDF" }}</span>
        </div>
        <div class="upload-actions">
          <button class="ghost" type="button" :disabled="!canUpload">选择文件</button>
        </div>
        <input ref="fileInput" class="hidden-input" type="file" accept="application/pdf" @change="onFileChange" />
      </div>
    </section>

    <section class="card settings-card">
      <h2>PDF处理设置</h2>
      <div class="settings-inline">
        <div class="settings-grid compact">
          <label>
            <span>每批处理页数</span>
            <div class="setting-control">
              <select v-model="translationBatchMode">
                <option v-for="option in translationBatchOptions" :key="option" :value="option">
                  {{ option === "custom" ? "自定义" : option === "all" ? "全部" : option }}
                </option>
              </select>
              <input
                v-if="translationBatchMode === 'custom'"
                type="number"
                min="1"
                class="short-input"
                v-model="translationCustomSize"
                placeholder=1
              />
            </div>
          </label>
          <label>
            <span>翻译范围页</span>
            <div class="setting-control">
              <select v-model="translationRangeMode">
                <option v-for="option in translationRangeOptions" :key="option" :value="option">
                  {{
                    option === "custom" ? "自定义" : option === "all" ? "全部" : option === "range" ? "区间" : option
                  }}
                </option>
              </select>
              <input
                v-if="translationRangeMode === 'custom'"
                type="number"
                min="1"
                class="short-input"
                v-model="translationRangeCustom"
                placeholder=1
              />
              <div v-if="translationRangeMode === 'range'" class="range-inputs">
                <input type="number" min="1" class="tiny-input" v-model="translationRangeStart" placeholder=1 />
                <span>~</span>
                <input type="number" min="1" class="tiny-input" v-model="translationRangeEnd" placeholder=1 />
              </div>
            </div>
          </label>
        </div>
        <div class="pagination pagination-inline">
          <button class="ghost" type="button" @click="goToPage(-1)" :disabled="currentPageIndex === 1">上一组</button>
          <span>
            第 {{ currentPageRange.start }} - {{ currentPageRange.end }} 页 / 共 {{ task?.pages.length || 0 }}
          </span>
          <button class="ghost" type="button" @click="goToPage(1)" :disabled="currentPageIndex === totalPageCount">
            下一组
          </button>
        </div>
      </div>
    </section>

    <section v-if="task" class="card selection-card">
      <div class="selection-toolbar">
        <span>已选择 {{ selectedPages.length }} 页</span>
        <div class="toolbar-actions">
          <button class="ghost" type="button" @click="selectVisiblePages" :disabled="!visiblePages.length">
            选中当前批
          </button>
          <button class="ghost" type="button" @click="selectAllPages" :disabled="!task?.pages.length">全选全部</button>
          <button class="ghost" type="button" @click="clearSelection" :disabled="!selectedPages.length">清空选择</button>
        </div>
      </div>
      <div class="selection-toolbar actions">
        <div class="toolbar-actions">
          <button class="ghost" type="button" @click="retryVisibleBatch" :disabled="!visiblePages.length || batchStatus.running">
            翻译当前批
          </button>
          <button class="ghost" type="button" @click="retrySelectedPages" :disabled="!selectedPages.length || batchStatus.running">
            翻译选中
          </button>
          <button class="ghost" type="button" @click="retryRangePages" :disabled="!task?.pages.length || batchStatus.running">
            按范围翻译
          </button>
          <button class="ghost" type="button" @click="retryVisibleFailedPages" :disabled="!visibleFailedPageNumbers.length || batchStatus.running" v-if="visibleFailedPageNumbers.length > 0">
            重试失败(当前页 {{ visibleFailedPageNumbers.length }})
          </button>
          <button class="ghost" type="button" @click="retryAllFailedPages" :disabled="!failedPageNumbers.length || batchStatus.running" v-if="visibleFailedPageNumbers.length > 0">
            重试失败 ({{ failedPageNumbers.length }})
          </button>
          <div class="batch-status" v-if="batchStatus.running">
            <button class="ghost" type="button" @click="toggleBatchPause">
              {{ batchStatus.paused ? "继续" : "暂停" }}
            </button>
          </div>
        </div>
        <div class="batch-status" v-if="batchStatus.running">
          <span v-if="batchStatus.paused">已暂停 · </span>
          正在翻译 {{ batchStatus.processed }}/{{ batchStatus.total }}
        </div>
        <div class="batch-status" v-else-if="batchStatus.message">
          {{ batchStatus.message }}
        </div>
      </div>
    </section>

    <section v-if="task" class="card">
      <div class="task-head">
        <div>
          <h2>{{ task.fileName }}</h2>
          <p class="muted">任务 ID：{{ task.id }} ｜ 页数：{{ task.totalPages }} ｜ 更新时间：{{ formatDate(task.updatedAt) }}</p>
        </div>
      <div class="task-actions">
        <button class="ghost" type="button" @click="runAiLayout" :disabled="layoutLoading || !providerReady || !task">
          {{ layoutLoading ? "AI 排版校对中..." : "AI 排版校对" }}
        </button>
          <div class="dropdown" ref="txtDropdownRef">
            <button class="ghost" type="button" @click="showTxtMenu = !showTxtMenu" :disabled="isExporting.txtOriginal || isExporting.txtFormatted">
              {{ isExporting.txtOriginal || isExporting.txtFormatted ? "生成 TXT..." : "导出 TXT" }}
            </button>
            <div class="dropdown-menu" v-if="showTxtMenu">
              <button
                type="button"
                class="ghost"
                :disabled="isExporting.txtOriginal"
                @click="exportTxt('original')"
              >
                {{ isExporting.txtOriginal ? "生成原版..." : "原版" }}
              </button>
              <button
                type="button"
                class="ghost"
                :disabled="isExporting.txtFormatted || !task.formattedByAI"
                @click="exportTxt('formatted')"
              >
                {{ isExporting.txtFormatted ? "生成排版..." : task.formattedByAI ? "AI排版" : "待生成" }}
              </button>
            </div>
          </div>
          <button class="ghost" type="button" :disabled="isExporting.pdf" @click="exportPdf">
            {{ isExporting.pdf ? "生成PDF..." : "导出PDF" }}
          </button>
        </div>
      </div>
      <div
        v-if="task && (task.formattingInProgress || (layoutNoticeVisible && layoutStatus !== 'idle'))"
        class="layout-status"
      >
        <div v-if="task.formattingInProgress" class="status running">
          <div class="layout-status-row">
            <span>AI 排版进行中...</span>
            <span class="layout-progress-label">{{ layoutProgressText }}</span>
          </div>
          <div class="layout-progress-bar">
            <div class="layout-progress-bar__value" :style="{ width: `${layoutProgressPercent}%` }"></div>
          </div>
        </div>
        <div v-else-if="layoutStatus === 'success' && layoutNoticeVisible" class="status success">
          <span>{{ layoutStatusMessage || "AI 排版完成，可导出排版版 TXT" }}</span>
          <button class="link-btn small" type="button" @click="dismissLayoutNotice">关闭</button>
        </div>
        <div v-else-if="layoutStatus === 'error' && layoutNoticeVisible" class="status error">
          <span>{{ layoutStatusMessage || "AI 排版失败，请重试" }}</span>
          <button class="link-btn small" type="button" @click="dismissLayoutNotice">关闭</button>
        </div>
      </div>
    </section>

    <section v-if="task" class="grid">
      <article v-for="page in visiblePages" :key="page.id" class="page-card">
        <header>
          <div class="page-title">
            <label class="checkbox">
              <input type="checkbox" :checked="isSelected(page.pageNumber)" @change="togglePageSelection(page.pageNumber)" />
              <span>第 {{ page.pageNumber }} 页</span>
            </label>
            <span class="badge" :class="`badge--${page.status}`">{{ statusLabel(page.status) }}</span>
          </div>
          <button
            type="button"
            class="ghost"
            @click="retryPages([page.pageNumber])"
            :disabled="!!retranslateLoading[page.pageNumber]"
          >
            {{ retranslateLoading[page.pageNumber] ? "翻译中..." : "重新翻译" }}
          </button>
        </header>

        <div class="image-box">
          <img v-if="page.imageUrl" :src="resolveAssetUrl(page.imageUrl)" :alt="`第${page.pageNumber}页`" />
        </div>

        <div class="text-block">
          <label>识别原文</label>
          <textarea readonly :value="page.sourceText || '未识别到有效文字'"></textarea>
        </div>

        <div class="text-block">
          <label>译文（简体中文）</label>
          <textarea
            v-model="page.translation"
            @input="handleManualTranslation(page)"
            :placeholder="page.hasText ? '译文为空' : '该页判定为纯图片'"
          ></textarea>
          <div class="text-actions">
            <button type="button" class="ghost" :disabled="!page.translation" @click="copyText(page.translation)">
              复制译文
            </button>
            <a v-if="page.textUrl" :href="resolveAssetUrl(page.textUrl)" target="_blank" rel="noopener">下载 TXT</a>
          </div>
        </div>

        <p v-if="page.error" class="error">翻译失败：{{ page.error }}</p>
        <p v-else-if="!page.hasText" class="muted small">未检测到文本，合并 TXT 时将跳过此页。</p>
      </article>
    </section>

    <section v-else class="card placeholder">
      <p>尚无任务结果，请先上传 PDF 或填入任务 ID。</p>
    </section>

    <div v-if="taskManagerVisible" class="settings-overlay" @click.self="closeTaskManager">
      <div class="task-manager modal-copy">
        <header class="task-manager-header">
          <div>
            <h3>任务管理</h3>
            <p class="muted small">查看历史任务，快速恢复或清理无用记录。</p>
          </div>
          <div class="task-manager-actions">
            <button class="ghost" type="button" @click="fetchTaskList({ silent: true })" :disabled="taskListLoading">
              {{ taskListLoading ? "刷新中..." : "刷新" }}
            </button>
            <button class="ghost" type="button" @click="closeTaskManager">关闭</button>
          </div>
        </header>
        <div class="task-manager-body">
          <div v-if="taskListLoading" class="task-list-placeholder">正在加载任务...</div>
          <div v-else-if="taskListError" class="task-list-placeholder error">{{ taskListError }}</div>
          <div v-else-if="!taskList.length" class="task-list-placeholder">暂无任务记录</div>
          <div v-else class="task-list-grid">
            <article
              v-for="item in taskList"
              :key="item.id"
              class="task-card"
              :class="`task-card--${taskSummaryStatus(item)}`"
              @click="handleTaskSelect(item)"
            >
              <header>
                <div>
                  <h4>{{ item.fileName }}</h4>
                  <p class="task-meta">任务 ID：{{ item.id }}</p>
                </div>
                <span class="badge" :class="`badge--${taskSummaryStatus(item)}`">{{ taskSummaryLabel(item) }}</span>
              </header>
              <p class="task-meta">更新时间：{{ formatDate(item.updatedAt) }}</p>
              <p class="task-progress">{{ taskProgressText(item) }}</p>
              <div class="task-card-actions">
                <button class="ghost" type="button" @click.stop="handleTaskSelect(item)">恢复</button>
                <button
                  class="ghost danger"
                  type="button"
                  @click.stop="deleteTaskEntry(item.id)"
                  :disabled="!!deletingTasks[item.id]"
                >
                  {{ deletingTasks[item.id] ? "删除中..." : "删除" }}
                </button>
              </div>
            </article>
          </div>
        </div>
      </div>
    </div>

    <div v-if="showSettings" class="settings-overlay" @click.self="closeSettings">
      <div class="settings-panel modal-copy">
        <header class="settings-header">
          <div>
            <div class="settings-header-caption"><h3>设置</h3></div>
          </div>
          <button class="btn-primary" type="button" @click="openProviderDialog()">
            <span class="btn-icon">＋</span>
            添加提供商
          </button>
        </header>

        <label class="backend-config">
          <span>后端 API Base</span>
          <input type="text" v-model="config.backendBase" placeholder="http://localhost:8090/api/pdf" />
        </label>

        <div v-if="!providerList.length" class="provider-empty">
          <div class="empty-icon">⚙️</div>
          <p>还没有配置任何提供商</p>
          <p class="muted">点击右上角按钮开始添加</p>
        </div>
        <div v-else class="provider-stack">
          <article v-for="provider in providerList" :key="provider.id" class="provider-card" :class="{ active: provider.id === activeProviderId }">
            <div class="provider-header-row">
              <label class="provider-toggle">
                <input type="checkbox" v-model="provider.enabled" @change="commitProvider(provider.id)" />
                <span>{{ provider.enabled ? "已启用" : "停用" }}</span>
              </label>
              <div class="provider-title">
                <h4>{{ provider.name }}</h4>
                <span class="tag brand">{{ getProviderLabel(provider.type) }}</span>
                <span class="tag success" v-if="provider.id === activeProviderId">当前使用</span>
                <span class="tag key" v-if="provider.apiKey">{{ maskKey(provider.apiKey) }}</span>
              </div>
              <div class="provider-actions">
                <button type="button" class="link-btn" @click="setActiveProvider(provider.id)">设为当前</button>
                <button type="button" class="link-btn" @click="openProviderDialog(provider)">编辑</button>
                <button type="button" class="link-btn danger" @click="deleteProvider(provider.id)">删除</button>
              </div>
            </div>

            <div class="provider-fields">
              <label>
                <span>API 密钥</span>
                <input type="password" :value="provider.apiKey" placeholder="未配置" readonly class="readonly-input" />
              </label>
              <label>
                <span>API URL（可选）</span>
                <input type="text" :value="provider.baseUrl" placeholder="https://..." readonly class="readonly-input" />
              </label>
            </div>

            <div class="models-head">
              <div>
                <strong>可用模型</strong>
              </div>
              <div class="models-actions">
                <button type="button" class="link-btn" @click="openModelDialog(provider.id)">添加模型</button>
              </div>
            </div>

            <div class="model-list modern">
              <div v-for="model in provider.models" :key="model.id" class="model-row">
                <div class="model-main">
                  <label class="provider-toggle small">
                    <input type="checkbox" v-model="model.enabled" @change="commitProvider(provider.id)" />
                    <span>{{ model.enabled ? "启用" : "停用" }}</span>
                  </label>
                  <div>
                    <div class="model-name">{{ model.name }}</div>
                  </div>
                </div>
                <div class="model-tags">
                  <span class="tag brand">{{ getModelApiLabel(model.apiType) }}</span>
                  <span class="tag success" v-if="model.name.toLowerCase().includes('image') || model.id.toLowerCase().includes('image')">图像</span>
                  <span class="tag">Max {{ model.maxTokens || DEFAULT_MAX_TOKENS }}</span>
                </div>
                <div class="model-row-actions">
                  <button
                    type="button"
                    class="link-btn"
                    @click="testModelConnection(provider.id, model.id)"
                    :disabled="getModelStatus(provider.id, model.id)?.status === 'testing'"
                  >
                    {{
                      getModelStatus(provider.id, model.id)?.status === "testing"
                        ? "测试中..."
                        : getModelStatus(provider.id, model.id)?.status === "success"
                          ? "重新测试"
                          : "测试连接"
                    }}
                  </button>
                  <button type="button" class="link-btn" @click="openModelDialog(provider.id, model)">编辑</button>
                  <button type="button" class="link-btn danger" @click="deleteModel(provider.id, model.id)">删除</button>
                </div>
                <p class="model-status success" v-if="getModelStatus(provider.id, model.id)?.status === 'success'">
                  已通过测试
                </p>
                <p class="model-status error" v-else-if="getModelStatus(provider.id, model.id)?.status === 'error'">
                  {{ getModelStatus(provider.id, model.id)?.message }}
                </p>
              </div>
              <p v-if="!provider.models.length" class="empty-hint">暂无模型，请先添加</p>
            </div>
          </article>
        </div>

        <div class="settings-footer">
          <button class="ghost" type="button" @click="closeSettings">取消</button>
          <button class="ghost primary" type="button" @click="handleSaveSettings">保存设置</button>
        </div>
      </div>

      <div class="dialog" v-if="providerDialogVisible">
        <div class="dialog-panel">
          <header>
            <h4>{{ editingProviderId ? "编辑提供商" : "新增提供商" }}</h4>
          </header>
          <div class="dialog-body">
            <label>
              <span>名称</span>
              <input type="text" v-model="providerForm.name" placeholder="例如：OpenAI 官方" />
            </label>
            <label>
              <span>类型</span>
              <select v-model="providerForm.type" @change="handleProviderTypeChange">
                <option v-for="option in providerTypeOptions" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
            </label>
            <label>
              <span>API Base</span>
              <input type="text" v-model="providerForm.baseUrl" placeholder="https://..." />
            </label>
            <label>
              <span>API Key</span>
              <input type="password" v-model="providerForm.apiKey" placeholder="sk-..." />
            </label>
          </div>
          <div class="dialog-actions">
            <button class="ghost" type="button" @click="providerDialogVisible = false">取消</button>
            <button class="ghost primary" type="button" @click="saveProvider">保存</button>
          </div>
        </div>
      </div>

      <div class="dialog" v-if="modelDialogVisible">
        <div class="dialog-panel">
          <header>
            <h4>{{ editingModelId ? "编辑模型" : "新增模型" }}</h4>
          </header>
          <div class="dialog-body">
            <label>
              <span>模型名称</span>
              <input type="text" v-model="modelForm.name" placeholder="例如：GPT-4o" />
            </label>
            <label class="model-id-block">
              <div class="label-row">
                <span>模型 ID</span>
                <button type="button" class="link-btn small" @click="fetchDialogModelList" :disabled="modelDialogState.loading">
                  {{ modelDialogState.loading ? "获取中..." : "获取模型列表" }}
                </button>
              </div>
              <input type="text" v-model="modelForm.id" placeholder="例如：deepseek-chat" />
            </label>
            <div class="dialog-model-selector" v-if="modelDialogState.available.length || modelDialogState.search || modelDialogState.error">
              <span class="model-picker-label">点击选择模型：</span>
              <input
                  type="text"
                  v-model="modelDialogState.search"
                  placeholder="输入关键词筛选模型"
                />
              <div v-if="filteredDialogModels.length" class="model-option-list">
                <button
                  type="button"
                  v-for="model in filteredDialogModels"
                  :key="model.id"
                  class="model-option"
                  @click="selectDialogModel(model)"
                >
                  <span class="model-option-id">{{ model.id }}</span>
                  <span class="model-option-name" v-if="model.name && model.name !== model.id">{{ model.name }}</span>
                </button>
              </div>
              <p v-else class="empty-hint">未找到匹配的模型</p>
              <p v-if="modelDialogState.error" class="error">{{ modelDialogState.error }}</p>
            </div>
            <label>
              <span>API 类型</span>
              <select v-model="modelForm.apiType">
                <option v-for="option in modelApiTypeOptions" :key="option.value" :value="option.value">
                  {{ getModelApiLabel(option.value) }}
                </option>
              </select>
            </label>
            <label>
              <span>最大输出 Token</span>
              <input type="number" min="1" class="short-input" v-model.number="modelForm.maxTokens" placeholder="默认 65535" />
            </label>
          </div>
          <div class="dialog-actions">
            <button class="ghost" type="button" @click="modelDialogVisible = false">取消</button>
            <button class="ghost primary" type="button" @click="saveModel">保存</button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="toast.visible" class="toast" :class="`toast--${toast.type}`">
      {{ toast.text }}
    </div>
  </div>
</template>

<style scoped>
:root {
  font-family: "PingFang SC", "Inter", system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  line-height: 1.5;
  font-weight: 400;
  color: #0f172a;
  background-color: #f4f6fb;
  font-synthesis: none;
  text-rendering: optimizeLegibility;
  -webkit-font-smoothing: antialiased;
}

*,
*::before,
*::after {
  box-sizing: border-box;
}

body {
  margin: 0;
  min-height: 100vh;
  background: radial-gradient(circle at top, #f9fbff, #eef2ff);
}

#app {
  max-width: 1200px;
  margin: 0 auto;
  padding: 32px 16px 60px;
}

.app {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.hero {
  background: linear-gradient(135deg, #eef2ff, #dde4ff);
  border-radius: 20px;
  padding: 24px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  box-shadow: 0 20px 60px rgba(15, 23, 42, 0.08);
}

.hero h1 {
  margin: 0;
  font-size: 28px;
}

.hero p {
  margin: 6px 0 0;
  color: #475569;
}

.hero-left {
  flex: 1;
}

.hero-right {
  display: flex;
  flex-direction: column;
  gap: 12px;
  align-items: flex-end;
}

.model-picker {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
}

.model-picker label {
  font-weight: 600;
  color: #475569;
}

.model-picker select {
  border: 1px solid #cbd5f5;
  border-radius: 8px;
  padding: 6px 16px;
  font-size: 14px;
  background: #fff;
}

.task-controls {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  justify-content: flex-end;
  align-items: center;
}

.task-controls input {
  border: 1px solid #cbd5f5;
  border-radius: 999px;
  padding: 8px 16px;
  font-size: 14px;
  min-width: 220px;
}

.card {
  background: #fff;
  border-radius: 18px;
  padding: 24px;
  box-shadow: 0 16px 50px rgba(15, 23, 42, 0.06);
}

.card h2 {
  margin: 0 0 10px;
  font-size: 20px;
}

.muted {
  color: #64748b;
  font-size: 14px;
}

.muted.small {
  font-size: 12px;
  color: #94a3b8;
}

.muted.warning {
  margin-top: 8px;
  color: #b45309;
}

.upload-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 18px;
  border: 1px dashed #cbd5f5;
  border-radius: 14px;
  margin-top: 12px;
  background: #f8fbff;
  cursor: pointer;
  transition: border-color 0.2s, background 0.2s;
}

.dropzone--active {
  border-color: #7c3aed;
  background: #f3e8ff;
}

.upload-info strong {
  display: block;
  font-size: 16px;
}

.upload-info span {
  color: #94a3b8;
  font-size: 13px;
}

.upload-actions {
  display: flex;
  gap: 12px;
}

.hidden-input {
  display: none;
}

.ghost {
  border: 1px solid #cbd5f5;
  border-radius: 999px;
  background: #fff;
  padding: 8px 18px;
  color: #0f172a;
  cursor: pointer;
  font-size: 14px;
  transition: all 0.2s ease;
}

.ghost:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.ghost:not(:disabled):hover {
  background: #eef2ff;
}

.ghost.primary {
  background: #2563eb;
  color: #fff;
  border-color: #1d4ed8;
}

.ghost.primary:hover {
  background: #1d4ed8;
}

.ghost.danger {
  border-color: #fecaca;
  color: #b91c1c;
}

.ghost.danger:hover:not(:disabled) {
  background: #fee2e2;
}

.settings-card .settings-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px;
}

.settings-grid.compact {
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  flex: 1;
}

.settings-inline {
  display: flex;
  gap: 16px;
  align-items: flex-end;
  flex-wrap: wrap;
}

.pagination-inline {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
  white-space: nowrap;
}

.setting-control {
  display: flex;
  gap: 8px;
  align-items: center;
}

.short-input {
  max-width: 90px;
}

.tiny-input {
  width: 80px;
}

.setting-control select,
.setting-control input {
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  padding: 8px 10px;
  font-size: 14px;
  flex: 1;
}

.settings-card .setting-control select {
  flex: 0 0 auto;
  min-width: 120px;
}

.setting-control input.short-input,
.setting-control input.tiny-input {
  flex: 0 0 auto;
}

.range-inputs {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
}

.range-inputs input {
  flex: 0 0 auto;
}

.pagination {
  margin-top: 16px;
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.selection-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.selection-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.toolbar-actions {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}

.batch-status {
  font-size: 13px;
  color: #0ea5e9;
}

.task-head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: center;
}

.task-actions {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}

.dropdown {
  position: relative;
  display: inline-block;
}

.dropdown-menu {
  position: absolute;
  top: calc(100% + 4px);
  right: 0;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  padding: 8px;
  box-shadow: 0 10px 25px rgba(15, 23, 42, 0.15);
  display: flex;
  flex-direction: column;
  gap: 6px;
  z-index: 20;
  min-width: 160px;
}

.dropdown-menu .ghost {
  width: 100%;
  justify-content: flex-start;
}

.ghost.success {
  background: #16a34a;
  color: #fff;
  border-color: #15803d;
}

.ghost.success:hover {
  background: #15803d;
}

.ghost.danger {
  background: #dc2626;
  color: #fff;
  border-color: #b91c1c;
}

.ghost.danger:hover {
  background: #b91c1c;
}

.layout-status {
  margin-top: 12px;
  font-size: 13px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.layout-status .status {
  padding: 12px 16px;
  border-radius: 14px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.layout-status .status.running {
  background: #fef3c7;
  color: #78350f;
}

.layout-status .status.success {
  background: #dcfce7;
  color: #166534;
}

.layout-status .status.error {
  background: #fee2e2;
  color: #b91c1c;
}

.layout-status-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: 600;
}

.layout-progress-label {
  font-size: 12px;
  color: #475569;
}

.layout-progress-bar {
  width: 100%;
  height: 6px;
  border-radius: 999px;
  background: rgba(15, 23, 42, 0.15);
  overflow: hidden;
}

.layout-progress-bar__value {
  height: 100%;
  border-radius: 999px;
  background: linear-gradient(90deg, #4f46e5, #14b8a6);
  transition: width 0.3s ease;
}

.links {
  margin-top: 12px;
  display: flex;
  gap: 16px;
}

.links a {
  color: #2563eb;
  font-size: 14px;
  text-decoration: none;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 20px;
}

.page-card {
  background: #fff;
  border-radius: 16px;
  padding: 18px;
  box-shadow: 0 12px 40px rgba(15, 23, 42, 0.05);
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.page-card header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}

.page-title {
  display: flex;
  align-items: center;
  gap: 8px;
}

.checkbox {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  color: #0f172a;
}

.badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;
  margin-left: 8px;
}

.badge--pending {
  background: #fef3c7;
  color: #c2410c;
}
.badge--completed {
  background: #dcfce7;
  color: #15803d;
}
.tag {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  background: #e2e8f0;
  color: #475569;
}

.tag.brand {
  background: #e0e7ff;
  color: #4338ca;
}

.tag.success {
  background: #dcfce7;
  color: #15803d;
}

.tag.key {
  background: #0f172a;
  color: #fff;
}
.badge--error {
  background: #fee2e2;
  color: #b91c1c;
}

.task-manager {
  width: min(960px, 100%);
  background: #fff;
  border-radius: 20px;
  padding: 28px;
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.task-manager-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.task-manager-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

.task-manager-body {
  min-height: 220px;
}

.task-list-placeholder {
  padding: 40px 0;
  text-align: center;
  color: #94a3b8;
  font-size: 14px;
}

.task-list-placeholder.error {
  color: #dc2626;
}

.task-list-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 16px;
}

.task-card {
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  cursor: pointer;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.task-card:hover {
  border-color: #94a3b8;
  box-shadow: 0 12px 24px rgba(15, 23, 42, 0.08);
}

.task-card header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: flex-start;
}

.task-card h4 {
  margin: 0;
  font-size: 16px;
  color: #0f172a;
}

.task-meta {
  margin: 0;
  font-size: 12px;
  color: #94a3b8;
  word-break: break-all;
}

.task-progress {
  margin: 0;
  font-size: 13px;
  color: #334155;
}

.task-card-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  margin-top: auto;
}

.task-card--pending {
  border-color: #fbbf24;
}

.task-card--error {
  border-color: #f87171;
}

.task-card--completed {
  border-color: #34d399;
}

.image-box {
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  padding: 8px;
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f8fafc;
}

.image-box img {
  max-width: 100%;
  border-radius: 8px;
}

.text-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.text-block textarea {
  width: 100%;
  min-height: 120px;
  border-radius: 10px;
  border: 1px solid #e2e8f0;
  padding: 10px;
  font-size: 14px;
  background: #fdfdfd;
  resize: vertical;
}

.text-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
}

.text-actions a {
  color: #2563eb;
  text-decoration: none;
}

.error {
  color: #b91c1c;
  font-size: 13px;
}

.small {
  font-size: 12px;
}

.placeholder {
  text-align: center;
  color: #94a3b8;
}

.toast {
  position: fixed;
  right: 32px;
  bottom: 32px;
  padding: 12px 18px;
  border-radius: 12px;
  color: #fff;
  box-shadow: 0 20px 60px rgba(15, 23, 42, 0.2);
}

.toast--success {
  background: #22c55e;
}

.toast--error {
  background: #ef4444;
}

.settings-overlay {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  padding: 24px;
}

.settings-panel.modal-copy {
  background: #fff;
  border-radius: 20px;
  width: min(1020px, 100%);
  max-height: 92vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 40px 120px rgba(15, 23, 42, 0.35);
  padding: 0 0 16px;
  overflow: hidden;
}

.settings-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20px 28px;
  border-bottom: 1px solid #e2e8f0;
  gap: 12px;
  background: linear-gradient(120deg, #eef2ff, #ffffff);
}

.settings-header-caption {
  font-size: 13px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: #94a3b8;
}

.settings-header h3 {
  margin: 4px 0 4px;
  font-size: 24px;
}

.settings-header p {
  margin: 0;
  color: #475569;
}

.btn-primary {
  border: none;
  border-radius: 999px;
  background: #2563eb;
  color: #fff;
  padding: 10px 18px;
  font-size: 14px;
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  box-shadow: 0 10px 30px rgba(37, 99, 235, 0.35);
}

.btn-primary:hover {
  background: #1d4ed8;
}

.btn-icon {
  font-size: 18px;
}

.backend-config {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 20px 28px 12px;
  font-size: 14px;
  color: #475569;
}

.backend-config input {
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  padding: 8px 12px;
  font-size: 14px;
}

.backend-config small {
  color: #94a3b8;
  font-size: 12px;
}

.provider-empty {
  margin: 0 28px;
  border: 1px dashed #cbd5f5;
  border-radius: 14px;
  padding: 28px;
  text-align: center;
  color: #94a3b8;
}

.empty-icon {
  font-size: 32px;
  margin-bottom: 6px;
}

.provider-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 0 28px;
  overflow-y: auto;
  max-height: 55vh;
}

.provider-card {
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 14px;
  background: #fff;
}

.provider-card.active {
  border-color: #2563eb;
  box-shadow: 0 0 0 1px rgba(37, 99, 235, 0.4);
}

.provider-header-row {
  display: grid;
  grid-template-columns: auto 1fr auto;
  gap: 12px;
  align-items: center;
}

.provider-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: #475569;
}

.provider-toggle input {
  accent-color: #2563eb;
}

.provider-toggle.small {
  font-size: 12px;
}

.provider-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.provider-title h4 {
  margin: 0;
}

.provider-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.link-btn {
  border: none;
  background: none;
  color: #2563eb;
  font-size: 13px;
  cursor: pointer;
  padding: 2px 4px;
}

.link-btn.small {
  font-size: 12px;
  padding: 0;
}

.link-btn.danger {
  color: #dc2626;
}

.provider-fields {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 12px;
}

.provider-fields label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  color: #475569;
}

.provider-fields input {
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  padding: 8px 10px;
  font-size: 14px;
  background: #fff;
}

.provider-fields .readonly-input {
  background: #f8fafc;
  color: #475569;
}

.models-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 10px;
  border-top: 1px dashed #e2e8f0;
  padding-top: 10px;
}

.models-head p {
  margin: 0;
  font-size: 12px;
  color: #94a3b8;
}

.models-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

.model-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.model-row {
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  padding: 10px;
  display: grid;
  grid-template-columns: 1.2fr auto auto;
  gap: 10px;
  align-items: center;
}

.model-main {
  display: flex;
  align-items: center;
  gap: 10px;
}

.model-tags {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.model-tags select {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 4px 8px;
  font-size: 12px;
  background: #fff;
}

.model-row-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  flex-wrap: wrap;
  align-items: center;
}

.model-status {
  grid-column: 1 / -1;
  font-size: 12px;
  margin: 0;
}

.model-status.success {
  color: #16a34a;
}

.model-status.error {
  color: #dc2626;
}

.settings-footer {
  margin: 12px 28px 0;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  border-top: 1px solid #e2e8f0;
  padding-top: 12px;
}

.dialog {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1050;
  padding: 16px;
}

.dialog-panel {
  width: min(420px, 100%);
  background: #fff;
  border-radius: 12px;
  padding: 18px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.dialog-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.model-id-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.dialog-model-selector {
  display: flex;
  flex-direction: column;
  gap: 10px;
  font-size: 13px;
  color: #475569;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 14px;
  padding: 12px;
}

.model-picker-label {
  font-size: 13px;
  color: #1e293b;
  font-weight: 600;
}

.model-search {
  display: flex;
  align-items: center;
  gap: 6px;
  border: 1px solid #d6def5;
  border-radius: 999px;
  background: #fff;
  padding: 4px 12px;
}

.model-search input {
  border: none;
  outline: none;
  flex: 1;
  font-size: 13px;
  background: transparent;
  color: #0f172a;
}

.model-search-icon {
  color: #94a3b8;
  font-size: 14px;
}

.model-option-list {
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  max-height: 160px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  background: #fff;
}

.model-option {
  border: none;
  background: transparent;
  text-align: left;
  padding: 8px 12px;
  font-size: 13px;
  color: #0f172a;
  cursor: pointer;
  border-bottom: 1px solid #f1f5f9;
  font-family: "JetBrains Mono", "SFMono-Regular", monospace;
}

.model-option:last-child {
  border-bottom: none;
}

.model-option:hover {
  background: #eef2ff;
}

.model-option-name {
  display: block;
  font-size: 12px;
  color: #94a3b8;
  font-family: "PingFang SC", "Inter", sans-serif;
  margin-top: 2px;
}

.model-option-id {
  display: block;
}

.dialog-body label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  color: #334155;
}

.dialog-body input,
.dialog-body select {
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  padding: 8px 10px;
  font-size: 14px;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

@media (max-width: 768px) {
  #app {
    padding: 20px 14px 80px;
  }

  .hero {
    flex-direction: column;
  }

  .hero-right {
    width: 100%;
    align-items: stretch;
  }

  .task-controls {
    flex-direction: column;
    align-items: stretch;
  }

  .task-controls input {
    width: 100%;
  }

  .upload-bar {
    flex-direction: column;
    gap: 12px;
    align-items: flex-start;
  }

  .settings-panel.modal-copy {
    width: min(100%, 640px);
  }
}
</style>
