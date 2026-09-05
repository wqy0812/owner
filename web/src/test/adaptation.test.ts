import { describe, expect, it } from 'vitest';
import { scenarioAdaptationIssues, environmentAdaptationIssues } from '../types/adaptation';
import { parseScenarioTemplate, serializeScenarioTemplate } from '../pages/scenarioTemplate';
import type { ComponentRelease } from '../types/domain';
const release = (os: string[]) => ({environmentConstraints:{os}} as unknown as ComponentRelease);
describe('adaptation contract',()=>{
 it('requires explicit scenario choices and rejects incompatible sets',()=>{expect(scenarioAdaptationIssues({},[{name:'A',release:release(['ubuntu'])}])).toHaveLength(1);expect(scenarioAdaptationIssues({},[{name:'A',release:release(['ubuntu'])}],false)).toEqual([]);expect(scenarioAdaptationIssues({os:['ubuntu','suse']},[{name:'A',release:release(['ubuntu'])}],false)).toHaveLength(1)});
 it('detects empty component intersection and missing actual facts',()=>{expect(scenarioAdaptationIssues({},[{name:'A',release:release(['ubuntu'])},{name:'B',release:release(['suse'])}],false)).toHaveLength(1);expect(environmentAdaptationIssues('S',{os:['ubuntu']},{})).toHaveLength(1);expect(environmentAdaptationIssues('S',{os:['ubuntu']},{os:'ubuntu'})).toEqual([])});
 it('round trips revision labels in templates',()=>{const template={nodes:[],edges:[],environmentConstraints:{os:['ubuntu']}};expect(parseScenarioTemplate(serializeScenarioTemplate(template))).toEqual(template)});
});
