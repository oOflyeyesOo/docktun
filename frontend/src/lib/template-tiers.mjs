// Runtime certification must come from an exact-recipe, platform-specific
// evidence record. Inclusion in a catalog alone is not certification.
export function templateTier(item) {
 if (item.unavailable) return {id:'unavailable',label:'Unavailable',description:item.unavailable}
 if (item.bundled) return {id:'bundled',label:'Bundled starter',description:'Maintained recipe; image/platform, storage and port checks run before deployment. Review application-specific setup.'}
 return {id:'community',label:'Community · review required',description:'External recipe, not runtime-certified by DockTun. Import is a draft; review images, privileges and storage before deploying.'}
}
