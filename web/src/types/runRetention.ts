export interface RetentionPolicy {
  autoArchive: boolean;
  autoCleanup: boolean;
  archiveDays: number;
  cleanupDays: number;
}
export interface ArchiveInfo {
  runId: string;
  status: string;
  source: string;
  actorId: string;
  error?: string;
  updatedAt: string;
  archivedAt?: string;
  sizeBytes: number;
  sha256?: string;
}
export interface CleanupItem {
  runId: string;
  eligible: boolean;
  reasons: string[];
}
export interface ArchiveHealth {
  cleanupResults?: Array<{
    runId: string;
    status: string;
    reason: string;
    at: string;
  }>;
  storageError?: string;
  configured: boolean;
  policy: RetentionPolicy;
  pending: number;
  sizeBytes: number;
  tasks: ArchiveInfo[];
  lastScanAt?: string;
  lastScanResult?: string;
}
