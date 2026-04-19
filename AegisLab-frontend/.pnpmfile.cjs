// .pnpmfile.cjs
function readPackage(pkg, context) {
  if (
    process.env.LOCAL_API === 'true' &&
    pkg.dependencies['@rcabench/client']
  ) {
    pkg.dependencies['@rcabench/client'] = 'link:../AegisLab/client/typescript';
  }
  return pkg;
}

module.exports = {
  hooks: {
    readPackage,
  },
};
