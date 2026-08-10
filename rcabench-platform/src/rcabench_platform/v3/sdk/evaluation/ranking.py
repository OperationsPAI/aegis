from typing import Literal

import polars as pl

from ..utils.dataframe import assert_columns

INDEX_COLUMNS = ["algorithm", "dataset", "datapack"]
SAMPLER_COLUMNS = ["sampler.name", "sampler.rate", "sampler.mode"]
AGG_LEVEL = Literal["algorithm", "dataset", "datapack", "sampler", "sampler_dataset"]


def agg_index(agg_level: AGG_LEVEL) -> list[str]:
    """Get the index columns for the specified aggregation level."""
    if agg_level == "datapack":
        return INDEX_COLUMNS
    elif agg_level == "dataset":
        return INDEX_COLUMNS[:-1]  # ["algorithm", "dataset"]
    elif agg_level == "algorithm":
        return INDEX_COLUMNS[:-2]  # ["algorithm"]
    elif agg_level == "sampler":
        return INDEX_COLUMNS + SAMPLER_COLUMNS
    elif agg_level == "sampler_dataset":
        # Include sampler info but aggregate at dataset level (no datapack)
        return INDEX_COLUMNS[:-1] + SAMPLER_COLUMNS  # ["algorithm", "dataset"] + sampler columns
    else:
        raise ValueError(f"Invalid agg_level: {agg_level}")


def calc_avg_runtime(df: pl.DataFrame, agg_level: AGG_LEVEL) -> pl.DataFrame:
    lf = df.lazy()

    # Select columns based on aggregation level
    is_sampler_level = agg_level in ["sampler", "sampler_dataset"]
    if is_sampler_level:
        columns_to_select = INDEX_COLUMNS + SAMPLER_COLUMNS
        group_by_columns = INDEX_COLUMNS + SAMPLER_COLUMNS
    else:
        columns_to_select = INDEX_COLUMNS
        group_by_columns = INDEX_COLUMNS

    lf = lf.select(*columns_to_select, pl.col("runtime.seconds"))

    lf = lf.group_by(group_by_columns).agg(pl.col("runtime.seconds").max())

    lf = lf.group_by(agg_index(agg_level)).agg(pl.col("runtime.seconds").mean().round(6).alias("runtime.seconds:avg"))

    df = lf.collect()
    return df


def calc_mrr(df: pl.DataFrame, agg_level: AGG_LEVEL) -> pl.DataFrame:
    """
    MRR: Mean Reciprocal Rank

    https://en.wikipedia.org/wiki/Mean_reciprocal_rank
    """

    lf = df.lazy()

    lf = lf.filter(pl.col("hit"))

    # Select columns based on aggregation level
    is_sampler_level = agg_level in ["sampler", "sampler_dataset"]
    if is_sampler_level:
        columns_to_select = INDEX_COLUMNS + SAMPLER_COLUMNS
        group_by_columns = INDEX_COLUMNS + SAMPLER_COLUMNS
    else:
        columns_to_select = INDEX_COLUMNS
        group_by_columns = INDEX_COLUMNS

    lf = lf.select(*columns_to_select, pl.col("rank"))

    lf = lf.group_by(group_by_columns).agg(pl.col("rank").min().alias("rank"))

    lf = lf.with_columns((1 / pl.col("rank")).alias("MRR"))

    agg_cols = [pl.col("MRR").mean().round(6)]

    if agg_level == "datapack":
        agg_cols = [pl.col("rank").first(), *agg_cols]

    lf = lf.group_by(agg_index(agg_level)).agg(*agg_cols)

    df = lf.collect()
    return df


def calc_accurary(df: pl.DataFrame, agg_level: AGG_LEVEL) -> pl.DataFrame:
    """
    AC@k: The probability that the top k results contain at least one relevant answer.

    Avg@k = sum(AC@i for i in 1..=i) / k

    https://dl.acm.org/doi/pdf/10.1145/3691620.3695065 S3.2.2
    """

    K = 5
    rangeK = range(1, K + 1)

    lf = df.lazy()

    hit_k = [(pl.col("hit") & (pl.col("rank") <= k)).alias(f"hit@{k}") for k in rangeK]

    # Select columns based on aggregation level
    is_sampler_level = agg_level in ["sampler", "sampler_dataset"]
    if is_sampler_level:
        columns_to_select = INDEX_COLUMNS + SAMPLER_COLUMNS
        group_by_columns = INDEX_COLUMNS + SAMPLER_COLUMNS
    else:
        columns_to_select = INDEX_COLUMNS
        group_by_columns = INDEX_COLUMNS

    lf = lf.select(*columns_to_select, *hit_k)

    lf = lf.group_by(group_by_columns).agg([pl.col(f"hit@{k}").any().cast(pl.Float64).alias(f"AC@{k}") for k in rangeK])

    lf = lf.group_by(agg_index(agg_level)).agg(
        *[pl.col(f"AC@{k}").sum().alias(f"AC@{k}.count") for k in rangeK],
        *[pl.col(f"AC@{k}").mean().alias(f"AC@{k}").round(6) for k in rangeK],
    )

    avg_k = [pl.mean_horizontal(pl.col(f"AC@{i}") for i in range(1, k + 1)).round(6).alias(f"Avg@{k}") for k in rangeK]

    lf = lf.with_columns(*avg_k)

    df = lf.collect()
    return df


def calc_precision(df: pl.DataFrame, agg_level: AGG_LEVEL) -> pl.DataFrame:
    """
    P@k: Precision at k.

    AP@k: Average Precision at k.

    https://sdsawtelle.github.io/blog/output/mean-average-precision-MAP-for-recommender-systems.html
    """

    K = 5
    rangeK = range(1, K + 1)

    lf = df.lazy()

    hit_k = [(pl.col("hit") & (pl.col("rank") <= k)).alias(f"hit@{k}") for k in rangeK]
    rel_k = [(pl.col("hit") & (pl.col("rank") == k)).alias(f"rel@{k}") for k in rangeK]

    # Select columns based on aggregation level
    is_sampler_level = agg_level in ["sampler", "sampler_dataset"]
    if is_sampler_level:
        columns_to_select = INDEX_COLUMNS + SAMPLER_COLUMNS
        group_by_columns = INDEX_COLUMNS + SAMPLER_COLUMNS
    else:
        columns_to_select = INDEX_COLUMNS
        group_by_columns = INDEX_COLUMNS

    lf = lf.select(*columns_to_select, *hit_k, *rel_k)

    p_k = [pl.col(f"hit@{k}").sum().cast(pl.Float64).truediv(k).alias(f"P@{k}") for k in rangeK]

    rel_k = [pl.col(f"rel@{k}").any().cast(pl.Float64).alias(f"rel@{k}") for k in rangeK]

    hit_k = [pl.col(f"hit@{k}").sum().cast(pl.Float64).alias(f"hit@{k}") for k in rangeK]

    lf = lf.group_by(group_by_columns).agg(*p_k, *rel_k, *hit_k)

    ap_k = [
        (
            pl.sum_horizontal(pl.col(f"P@{i}") * pl.col(f"rel@{i}") for i in range(1, k + 1))
            .truediv(pl.col(f"hit@{k}"))
            .fill_nan(0)
            .round(6)
            .alias(f"AP@{k}")
        )
        for k in rangeK
    ]

    lf = (
        lf.with_columns(*ap_k)
        .drop([f"rel@{k}" for k in rangeK], strict=True)
        .drop([f"hit@{k}" for k in rangeK], strict=True)
    )

    if agg_level != "datapack":
        map_k = [pl.col(f"AP@{k}").mean().round(6).alias(f"MAP@{k}") for k in rangeK]
        lf = lf.group_by(agg_index(agg_level)).agg(*map_k)

    df = lf.collect()
    return df


def calc_index(df: pl.DataFrame, agg_level: AGG_LEVEL) -> pl.DataFrame:
    lf = df.lazy()

    lf = lf.with_columns(pl.col("exception.type").is_not_null().cast(pl.UInt32).alias("error"))

    # Select columns based on aggregation level
    is_sampler_level = agg_level in ["sampler", "sampler_dataset"]
    if is_sampler_level:
        columns_to_select = INDEX_COLUMNS + SAMPLER_COLUMNS + ["error"]
        unique_subset = INDEX_COLUMNS + SAMPLER_COLUMNS
    else:
        columns_to_select = INDEX_COLUMNS + ["error"]
        unique_subset = INDEX_COLUMNS

    lf = lf.select(*columns_to_select).unique(subset=unique_subset)

    lf = lf.group_by(agg_index(agg_level)).agg(
        pl.len().cast(pl.UInt32).alias("total"),
        pl.col("error").sum().cast(pl.UInt32).alias("error"),
    )

    df = lf.collect()
    return df


def calc_all_perf(df: pl.DataFrame, *, agg_level: AGG_LEVEL, include_sampled: bool = False) -> pl.DataFrame:
    # Check if sampler columns exist in the dataframe
    has_sampler_columns = all(col in df.columns for col in SAMPLER_COLUMNS)

    # For sampler aggregation levels, we need sampler columns
    if agg_level in ["sampler", "sampler_dataset"]:
        if not include_sampled or not has_sampler_columns:
            raise ValueError(f"Aggregation level '{agg_level}' requires include_sampled=True and sampler columns")
        assert_columns(df, INDEX_COLUMNS + SAMPLER_COLUMNS)
    else:
        assert_columns(df, INDEX_COLUMNS)
        # Filter out sampled data if not including sampled results
        if has_sampler_columns and not include_sampled:
            df = df.filter(pl.col("sampler.name").is_null())

    assert_columns(df, ["hit", "rank", "runtime.seconds", "exception.type"])

    # Get the correct index columns for joins
    index = agg_index(agg_level)

    ans = calc_index(df, agg_level)

    ans = ans.join(calc_avg_runtime(df, agg_level), on=index, how="left")
    ans = ans.join(calc_mrr(df, agg_level), on=index, how="left")
    ans = ans.join(calc_accurary(df, agg_level), on=index, how="left")
    ans = ans.join(calc_precision(df, agg_level), on=index, how="left")

    ans = ans.fill_null(strategy="zero")

    # Sort by appropriate columns
    if agg_level == "datapack":
        ans = ans.sort(by=["algorithm", "rank", "dataset", "datapack"])
    elif agg_level == "dataset":
        ans = ans.sort(by=["algorithm", "dataset"])
    elif agg_level == "sampler":
        ans = ans.sort(by=["algorithm", "dataset", "datapack", "sampler.name", "sampler.rate"])
    elif agg_level == "sampler_dataset":
        ans = ans.sort(by=["algorithm", "dataset", "sampler.name", "sampler.rate"])

    return ans


def calc_all_perf_by_datapack_attr(df: pl.DataFrame, dataset: str, attr_col: str) -> pl.DataFrame:
    assert df.filter(pl.col("dataset") != dataset).is_empty()

    df = df.drop("dataset").with_columns(pl.col(attr_col).alias("dataset"))

    perf_df = calc_all_perf(df, agg_level="dataset")

    perf_df = perf_df.with_columns(pl.col("dataset").alias(attr_col))

    perf_df = perf_df.select(
        "algorithm",
        pl.lit(dataset).alias("dataset"),
        attr_col,
        pl.all().exclude("algorithm", "dataset", attr_col),
    )

    return perf_df
