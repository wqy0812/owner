export interface ComponentUsage {
  componentCount: number;
  scenarioCount: number;
  components: Array<{
    componentId: string;
    name: string;
    releaseId: string;
    version: string;
    lineName: string;
    status: string;
    ownerName: string;
    upstreamReleaseId: string;
    upstreamVersion: string;
    dependencyKind: string;
    canViewDetails: boolean;
  }>;
  scenarios: Array<{
    scenarioId: string;
    name: string;
    revisionId: string;
    revision: number;
    status: string;
    ownerName: string;
    releaseId: string;
    version: string;
    current: boolean;
    references: number;
    canViewDetails: boolean;
  }>;
}
