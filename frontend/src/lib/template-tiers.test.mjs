import test from 'node:test'
import assert from 'node:assert/strict'
import {templateTier} from './template-tiers.mjs'
test('catalog inclusion cannot claim runtime certification',()=>{
 assert.equal(templateTier({bundled:true}).id,'bundled')
 assert.equal(templateTier({source:'trusted-looking title'}).id,'community')
 assert.equal(templateTier({bundled:true,unavailable:'missing image'}).id,'unavailable')
})
