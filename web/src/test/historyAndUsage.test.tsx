import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import {
  ComponentUsagePanel,
} from "../components/ComponentUsagePanel";
import { RunCleanupModal } from "../components/RunCleanupModal";
import { RunRetentionPanel } from "../components/RunRetentionPanel";
import { api } from "../api/client";
import type { Component, RunSummary } from "../types/domain";

import type { ComponentUsage } from '../types/componentUsage';

const app = vi.hoisted(() => ({
  user: { id: "owner", role: "platform_admin" },
  refreshTokens: {},
  notify: vi.fn(),
  signalRefresh: vi.fn(),
}));
vi.mock("../context/AppContext", () => ({
  useApp: () => app,
  displayError: (e: Error) => e.message,
}));
const component = {
  id: "upstream",
  releases: [{ id: "v1", version: "1", lineName: "Main" }],
} as Component;
const empty: ComponentUsage = {
  componentCount: 0,
  scenarioCount: 0,
  components: [],
  scenarios: [],
};
const usage: ComponentUsage = {
  componentCount: 1,
  scenarioCount: 1,
  components: [
    {
      componentId: "private",
      name: "Private",
      releaseId: "private-v1",
      version: "1",
      lineName: "Main",
      status: "draft",
      ownerName: "Owner",
      upstreamReleaseId: "v1",
      upstreamVersion: "1",
      dependencyKind: "configuration",
      canViewDetails: false,
    },
  ],
  scenarios: [
    {
      scenarioId: "scene",
      name: "Scene",
      revisionId: "r1",
      revision: 1,
      status: "released",
      ownerName: "Scene owner",
      releaseId: "v1",
      version: "1",
      current: true,
      references: 2,
      canViewDetails: true,
    },
  ],
};
beforeEach(() => {
  vi.restoreAllMocks();
  app.notify.mockClear();
  app.signalRefresh.mockClear();
  vi.spyOn(api, "componentUsage").mockResolvedValue(usage);
  vi.spyOn(api, "archiveHealth").mockResolvedValue({
    configured: true,
    pending: 0,
    sizeBytes: 0,
    tasks: [],
    policy: {
      autoArchive: false,
      autoCleanup: false,
      archiveDays: 90,
      cleanupDays: 90,
    },
  });
});
it("filters exact versions and history, keeps private summaries private and links exact revisions", async () => {
  render(
    <MemoryRouter>
      <ComponentUsagePanel component={component} />
    </MemoryRouter>,
  );
  expect(await screen.findByText("下游组件（1）")).toBeInTheDocument();
  expect(screen.getByText("引用 2 次", { exact: false })).toBeInTheDocument();
  expect(screen.getByText("仅引用摘要")).toBeInTheDocument();
  expect(
    within(screen.getByText("Private · 1").closest("article")!).queryByRole("link", { name: "查看版本" }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: "查看版本" })).toHaveAttribute(
    "href",
    "/scenarios?selected=scene&revision=r1",
  );
  fireEvent.change(screen.getByLabelText("组件版本"), {
    target: { value: "v1" },
  });
  fireEvent.click(screen.getByLabelText("包含历史引用"));
  await waitFor(() =>
    expect(api.componentUsage).toHaveBeenLastCalledWith(
      "upstream",
      "v1",
      true,
      expect.any(AbortSignal),
    ),
  );
});
it("retries failed requests and ignores an old component response after switching", async () => {
  vi.mocked(api.componentUsage).mockRejectedValueOnce(new Error("Load failed"));
  const view = render(
    <MemoryRouter>
      <ComponentUsagePanel key="first" component={component} />
    </MemoryRouter>,
  );
  expect(await screen.findByText("Load failed")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /重试/ }));
  expect(await screen.findByText("Private · 1")).toBeInTheDocument();
  let resolveOld!: (u: ComponentUsage) => void;
  vi.mocked(api.componentUsage)
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveOld = resolve;
        }),
    )
    .mockResolvedValueOnce(empty);
  fireEvent.change(screen.getByLabelText("组件版本"), {
    target: { value: "v1" },
  });
  view.rerender(
    <MemoryRouter>
      <ComponentUsagePanel
        key="second"
        component={{ ...component, id: "other" }}
      />
    </MemoryRouter>,
  );
  expect(await screen.findByText("暂无下游组件")).toBeInTheDocument();
  await act(async () => resolveOld(usage));
  expect(screen.queryByText("Private · 1")).not.toBeInTheDocument();
  expect(screen.getByLabelText("组件版本")).toHaveValue("");
});
const runs = [
  {
    id: "success",
    name: "Success",
    status: "succeeded",
    finishedAt: "2026-01-01",
  },
  {
    id: "failure",
    name: "Failure",
    status: "failed",
    finishedAt: "2026-01-01",
  },
] as RunSummary[];
it("archives only explicitly selected successful rows and clears selection on page changes", async () => {
  vi.spyOn(api, "archiveRuns").mockResolvedValue([]);
  const view = render(<RunRetentionPanel runs={runs} onDeleted={vi.fn()} />);
  await screen.findByText("归档目录已配置");
  fireEvent.click(screen.getByLabelText(/Success/));
  fireEvent.click(screen.getByRole("button", { name: "归档所选成功记录" }));
  await waitFor(() =>
    expect(api.archiveRuns).toHaveBeenCalledWith(["success"]),
  );
  await waitFor(() =>
    expect(screen.getByLabelText(/Success/)).not.toBeDisabled(),
  );
  view.rerender(<RunRetentionPanel runs={[runs[1]]} onDeleted={vi.fn()} />);
  expect(
    screen.getByRole("button", { name: "归档所选成功记录" }),
  ).toBeDisabled();
});
it("requires a complete eligible preview and explicit confirmation before cleanup", async () => {
  vi.spyOn(api, "cleanupPreview")
    .mockResolvedValueOnce([
      { runId: "failure", eligible: false, reasons: ["被回滚备份引用"] },
    ])
    .mockResolvedValueOnce([{ runId: "failure", eligible: true, reasons: [] }]);
  vi.spyOn(api, "cleanupRuns").mockResolvedValue(undefined);
  render(<RunRetentionPanel runs={runs} onDeleted={vi.fn()} />);
  await screen.findByText("归档目录已配置");
  fireEvent.click(screen.getByLabelText(/Failure/));
  fireEvent.click(screen.getByRole("button", { name: "删除所选失败记录" }));
  expect(await screen.findByText(/被回滚备份引用/)).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "确认删除" }),
  ).toBeDisabled();
  expect(api.cleanupRuns).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  fireEvent.click(screen.getByRole("button", { name: "删除所选失败记录" }));
  await screen.findByText(/可以删除/);
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "确认删除" }),
    ).not.toBeDisabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认删除" }));
  await waitFor(() =>
    expect(api.cleanupRuns).toHaveBeenCalledWith(["failure"]),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  expect(app.signalRefresh).toHaveBeenCalledWith(
    expect.arrayContaining(["runs", "workbench", "notifications"]),
  );
});

it("blocks deletion when the preview is incomplete or refers to different runs", async () => {
  vi.spyOn(api, "cleanupPreview").mockResolvedValue([{ runId: "other", eligible: true, reasons: [] }]);
  vi.spyOn(api, "cleanupRuns").mockResolvedValue(undefined);
  render(<RunCleanupModal runIds={["failure"]} onClose={vi.fn()} onDeleted={vi.fn()} />);
  await screen.findByText("可以删除");
  expect(screen.getByRole("button", { name: "确认删除" })).toBeDisabled();
  expect(api.cleanupRuns).not.toHaveBeenCalled();
});
it("rechecks protection after a deletion conflict and keeps the confirmation open", async () => {
  vi.spyOn(api, "cleanupPreview").mockResolvedValueOnce([{ runId: "failure", eligible: true, reasons: [] }]).mockResolvedValueOnce([{ runId: "failure", eligible: false, reasons: ["被新的续跑引用"] }]);
  vi.spyOn(api, "cleanupRuns").mockRejectedValue(new Error("引用已变化"));
  const deleted = vi.fn();
  render(<RunCleanupModal runIds={["failure"]} onClose={vi.fn()} onDeleted={deleted} />);
  await screen.findByText("可以删除");
  fireEvent.click(screen.getByRole("button", { name: "确认删除" }));
  await screen.findByText("被新的续跑引用");
  expect(screen.getByRole("button", { name: "确认删除" })).toBeDisabled();
  expect(deleted).not.toHaveBeenCalled();
});
