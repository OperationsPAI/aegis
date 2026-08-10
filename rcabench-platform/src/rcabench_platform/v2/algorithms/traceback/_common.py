from ...graphs.sdg.build_ import build_sdg
from ...graphs.sdg.defintion import SDG
from ...utils.env import debug, getenv_int
from ...utils.fs import has_recent_file
from ...utils.serde import load_pickle, save_pickle
from ..spec import AlgorithmArgs

if debug():
    _DEFAULT_SDG_CACHE_TIME = 600
else:
    _DEFAULT_SDG_CACHE_TIME = 0

SDG_CACHE_TIME = getenv_int("SDG_CACHE_TIME", default=_DEFAULT_SDG_CACHE_TIME)


def build_sdg_with_cache(args: AlgorithmArgs) -> SDG:
    sdg_pkl_path = args.output_folder / "sdg.pkl"

    if SDG_CACHE_TIME:
        if has_recent_file(sdg_pkl_path, seconds=SDG_CACHE_TIME):
            return load_pickle(path=sdg_pkl_path)

    sdg = build_sdg(args.dataset, args.datapack, args.input_folder)

    if SDG_CACHE_TIME:
        save_pickle(sdg, path=sdg_pkl_path)

    return sdg
