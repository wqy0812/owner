import copy
import sys
import unittest

sys.path.insert(0, sys.argv.pop(1))
import cf_media


class MediaPolicyTest(unittest.TestCase):
    def setUp(self):
        self.calls = []
        cf_media.artifact = lambda *args: self.calls.append(args[0])
        cf_media.image = lambda *args: self.calls.append(args[0])
        cf_media.request = self.unexpected_transfer
        cf_media.docker = self.unexpected_transfer
        self.plan = {
            'deliveryRequirements': [{'id': 'a', 'kind': 'artifact', 'identity': 'sha256:fixture',
                                      'source': 'https://source/a', 'target': 'https://target/a'}],
            'deliveryDecisions': [{'requirementId': 'a', 'mode': 'transfer', 'policy': 'transfer_to_target'}],
        }

    def unexpected_transfer(self, *args):
        self.calls.append(args)
        raise AssertionError('invalid plan reached a transfer')

    def test_target_only(self):
        cf_media.prepare(self.plan)
        self.assertEqual(self.calls, ['https://target/a'])

    def test_invalid_plans_fail_before_side_effects(self):
        changes = [
            lambda p: p.update(deliveryDecisions=[]),
            lambda p: p['deliveryDecisions'][0].update(mode='direct'),
            lambda p: p['deliveryDecisions'][0].update(policy=''),
            lambda p: p['deliveryDecisions'][0].update(policy='other'),
            lambda p: p['deliveryRequirements'][0].update(target=' '),
            lambda p: p['deliveryRequirements'][0].update(kind='unknown'),
            lambda p: p['deliveryDecisions'].append(p['deliveryDecisions'][0]),
            lambda p: p['deliveryRequirements'].append(p['deliveryRequirements'][0]),
            lambda p: p['artifactTransfers'][0].update(requirementId='orphan'),
        ]
        for change in changes:
            plan = copy.deepcopy(self.plan)
            plan['artifactTransfers'] = [{'requirementId': 'a', 'targetStation': 'target', 'relativePath': 'a', 'sha256': 'fixture'}]
            change(plan)
            with self.assertRaises(ValueError):
                cf_media.prepare(plan)
            self.assertEqual(self.calls, [])


if __name__ == '__main__':
    unittest.main()
