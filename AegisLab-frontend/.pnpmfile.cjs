// .pnpmfile.cjs
function readPackage(pkg, context) {
  if (
    process.env.LOCAL_API === 'true' &&
    pkg.dependencies['@OperationsPAI/portal']
  ) {
    pkg.dependencies['@OperationsPAI/portal'] = 'link:../AegisLab/sdk/typescript/portal';
  }
  return pkg;
}

module.exports = {
  hooks: {
    readPackage,
  },
};
